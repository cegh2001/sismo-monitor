package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"
	"sismo-monitor/internal/alert"
)

func TestBuildPrompt(t *testing.T) {
	req := SynthesisRequest{
		TriggerType:    "INESTABILIDAD_CORTICAL",
		FaultName:      "Falla de El Pilar",
		CellID:         "G_22_12",
		BValue:         0.78,
		WeightedEnergy: 1.45e12,
		DynamicRate:    3.2,
		Mainshock: alert.Sismo{
			Magnitude: 4.8,
			Depth:     8.7,
			Location:  "Near Coast of Venezuela",
			Time:      time.Date(2026, 7, 19, 4, 54, 0, 0, time.UTC),
		},
		Phase: alert.PhasePrecursor,
	}

	prompt, err := BuildPrompt(req)
	if err != nil {
		t.Fatalf("BuildPrompt failed: %v", err)
	}

	if len(prompt) == 0 {
		t.Fatal("Expected non-empty prompt")
	}

	if !containsString(prompt, "Falla de El Pilar") {
		t.Errorf("Expected prompt to contain fault name, got:\n%s", prompt)
	}

	if !containsString(prompt, "INESTABILIDAD_CORTICAL") {
		t.Errorf("Expected prompt to contain trigger type, got:\n%s", prompt)
	}
}

func TestRateLimitLogic(t *testing.T) {
	synth := NewGemmaSynthesizer("dummy-key", nil)
	now := time.Now()

	// First attempt should pass check (isManual = false)
	if err := synth.checkRateLimit("G_1_1", false, now); err != nil {
		t.Fatalf("Expected no error on first check, got: %v", err)
	}

	// Record success
	synth.updateRateLimit("G_1_1", now)

	// Second immediate check for same cell must fail due to cell cooldown when isManual = false
	if err := synth.checkRateLimit("G_1_1", false, now.Add(1*time.Second)); err == nil {
		t.Errorf("Expected error on immediate repeat for same cell, got nil")
	}

	// Manual request (isManual = true) for same cell must BYPASS cell cooldown (after 11s global cooldown)
	if err := synth.checkRateLimit("G_1_1", true, now.Add(11*time.Second)); err != nil {
		t.Errorf("Expected manual trigger to bypass cell cooldown, got: %v", err)
	}

	// Check for a different cell immediately must fail due to global cooldown (10s)
	if err := synth.checkRateLimit("G_2_2", false, now.Add(1*time.Second)); err == nil {
		t.Errorf("Expected error on global cooldown, got nil")
	}

	// Check after global cooldown passes (11s) for new cell must succeed
	if err := synth.checkRateLimit("G_2_2", false, now.Add(11*time.Second)); err != nil {
		t.Errorf("Expected success for different cell after global cooldown, got: %v", err)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > len(sub) && findString(s, sub)))
}

func findString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBuildThinkingConfigPerModelFamily(t *testing.T) {
	synth := NewGemmaSynthesizer("dummy-key", nil)

	flashCfg := synth.buildThinkingConfig("gemini-2.5-flash")
	if flashCfg.ThinkingBudget == nil || *flashCfg.ThinkingBudget != Gemini25ThinkingBudget {
		t.Errorf("gemini-2.5 should use thinkingBudget %d, got %v", Gemini25ThinkingBudget, flashCfg.ThinkingBudget)
	}
	if flashCfg.ThinkingLevel != "" {
		t.Errorf("gemini-2.5 must not set ThinkingLevel, got %q", flashCfg.ThinkingLevel)
	}

	minimalModels := []string{
		"gemini-3.5-flash",
		"gemini-3-flash-preview",
		"gemini-3.5-flash-lite",
		"gemini-3.1-flash-lite",
		"gemma-4-31b-it",
		"gemma-4-26b-a4b-it",
	}
	for _, model := range minimalModels {
		t.Run(model, func(t *testing.T) {
			cfg := synth.buildThinkingConfig(model)
			if cfg.ThinkingLevel != genai.ThinkingLevelMinimal {
				t.Errorf("%s should use MINIMAL thinking, got %q", model, cfg.ThinkingLevel)
			}
			if cfg.ThinkingBudget != nil {
				t.Errorf("%s must not set ThinkingBudget, got %d", model, *cfg.ThinkingBudget)
			}
		})
	}
}

func TestIsFatalError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"401 auth", fmt.Errorf("Error 401, Message: API key not valid"), true},
		{"403 permission", fmt.Errorf("Error 403, Message: PERMISSION_DENIED"), true},
		{"cancelled ctx", context.Canceled, true},
		{"400 validation", fmt.Errorf("Error 400, Message: invalid argument"), false},
		{"503 timeout", fmt.Errorf("Error 503, Message: The request timed out"), false},
		{"429 quota", fmt.Errorf("Error 429, Message: RESOURCE_EXHAUSTED"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isFatalError(tc.err); got != tc.want {
			t.Errorf("%s: isFatalError=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSynthesizeFallsBackOn400 verifies a non-transient validation error
// (e.g. an unsupported tool) falls through to the next grounded model instead
// of aborting the whole chain.
func TestSynthesizeFallsBackOn400(t *testing.T) {
	hits := runFallbackScenario(t, 400, `{"error":{"code":400,"message":"invalid argument","status":"INVALID_ARGUMENT"}}`)
	assertFallbackHits(t, hits)
}

// TestSynthesizeFallsBackOn503 verifies a transient server error (after the
// retry budget is exhausted) also falls through to the next grounded model.
func TestSynthesizeFallsBackOn503(t *testing.T) {
	hits := runFallbackScenario(t, 503, `{"error":{"code":503,"message":"The request timed out. Please try again.","status":"UNAVAILABLE"}}`)
	assertFallbackHits(t, hits)
}

// newMockSynthesizer spins up an httptest Gemini API server using handler and
// returns a GemmaSynthesizer wired to it with fast retry backoff.
func newMockSynthesizer(t *testing.T, handler http.HandlerFunc) (*GemmaSynthesizer, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Backend:     genai.BackendGeminiAPI,
		APIKey:      "test-key",
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL + "/"},
	})
	if err != nil {
		srv.Close()
		t.Fatalf("failed to create test client: %v", err)
	}
	synth := NewGemmaSynthesizer("test-key", nil)
	synth.client = client
	synth.retryBackoff = time.Millisecond
	return synth, srv.Close
}

// runFallbackScenario spins up a mock Gemini API that fails the primary
// grounded model with failStatus and succeeds for any other model, then runs a
// manual synthesis and returns the ordered list of requested models.
func runFallbackScenario(t *testing.T, failStatus int, failBody string) []string {
	t.Helper()

	var hits []string
	synth, cleanup := newMockSynthesizer(t, func(w http.ResponseWriter, r *http.Request) {
		model := modelFromPath(r.URL.Path)
		hits = append(hits, model)

		if model == geminiCascade[0].Name {
			w.WriteHeader(failStatus)
			_, _ = io.WriteString(w, failBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"CLASIFICACION: CALMA_REAJUSTE\n\nResumen Ejecutivo: sin cambios significativos."}]},"finishReason":"STOP","index":0}]}`)
	})
	defer cleanup()

	resp, err := synth.Synthesize(context.Background(), SynthesisRequest{
		TriggerType: "TEST",
		CellID:      "G_1_1",
		IsManual:    true,
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	wantModel := geminiCascade[1].Name
	if !geminiCascade[1].Grounded {
		wantModel += " (no grounding)"
	}
	if resp.ModelUsed != wantModel {
		t.Errorf("expected fallback model %q, got %q", wantModel, resp.ModelUsed)
	}
	if strings.TrimSpace(resp.Body) == "" {
		t.Error("expected non-empty report body")
	}
	return hits
}

func assertFallbackHits(t *testing.T, hits []string) {
	t.Helper()
	if len(hits) == 0 {
		t.Fatal("expected at least one request")
	}
	if hits[0] != geminiCascade[0].Name {
		t.Errorf("first request should target %q, got %q", geminiCascade[0].Name, hits[0])
	}
	for _, h := range hits {
		if h == geminiCascade[1].Name {
			return
		}
	}
	t.Errorf("expected a request to the fallback model %q, hits=%v", geminiCascade[1].Name, hits)
}

func TestSynthesizeGeminiFallsBackToGemmaReserve(t *testing.T) {
	var hits []string
	synth, cleanup := newMockSynthesizer(t, func(w http.ResponseWriter, r *http.Request) {
		model := modelFromPath(r.URL.Path)
		hits = append(hits, model)

		if strings.HasPrefix(model, "gemini-") {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"CLASIFICACION: CALMA_REAJUSTE\n\nResumen Ejecutivo: reserva gemma operativa."}]},"finishReason":"STOP","index":0}]}`)
	})
	defer cleanup()

	resp, err := synth.Synthesize(context.Background(), SynthesisRequest{
		TriggerType: "TEST",
		CellID:      "G_1_1",
		IsManual:    true,
	})
	if err != nil {
		t.Fatalf("expected Gemma reserve fallback to succeed, got error: %v", err)
	}
	if resp.ModelUsed != gemmaCascade[0].Name {
		t.Errorf("expected Gemma reserve model %q, got %q", gemmaCascade[0].Name, resp.ModelUsed)
	}
	if len(hits) != len(geminiCascade)+1 {
		t.Errorf("expected %d attempts (%d Gemini + 1 Gemma), got %d (%v)", len(geminiCascade)+1, len(geminiCascade), len(hits), hits)
	}
}

func TestSynthesizeGemmaEngineDirect(t *testing.T) {
	var hits []string
	synth, cleanup := newMockSynthesizer(t, func(w http.ResponseWriter, r *http.Request) {
		model := modelFromPath(r.URL.Path)
		hits = append(hits, model)

		if model == gemmaCascade[0].Name {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"The request timed out. Please try again.","status":"UNAVAILABLE"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"CLASIFICACION: CONFIRMACION\n\nResumen Ejecutivo: respuesta gemma 26b."}]},"finishReason":"STOP","index":0}]}`)
	})
	defer cleanup()

	resp, err := synth.Synthesize(context.Background(), SynthesisRequest{
		TriggerType: "TEST",
		CellID:      "G_1_1",
		IsManual:    true,
		Engine:      EngineGemma,
	})
	if err != nil {
		t.Fatalf("expected Gemma direct engine fallback to succeed, got error: %v", err)
	}
	if resp.ModelUsed != gemmaCascade[1].Name {
		t.Errorf("expected %q, got %q", gemmaCascade[1].Name, resp.ModelUsed)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h, "gemma-") {
			t.Errorf("Gemma engine must only call gemma-* models, saw %q in %v", h, hits)
		}
	}
}

func modelFromPath(path string) string {
	const marker = "/models/"
	idx := strings.Index(path, marker)
	if idx == -1 {
		return ""
	}
	rest := path[idx+len(marker):]
	return strings.SplitN(rest, ":", 2)[0]
}
