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

	gemmaCfg := synth.buildThinkingConfig("gemma-4-31b-it")
	if gemmaCfg.ThinkingLevel != genai.ThinkingLevelMinimal {
		t.Errorf("gemma should use MINIMAL thinking, got %q", gemmaCfg.ThinkingLevel)
	}
	if gemmaCfg.ThinkingBudget != nil {
		t.Error("gemma must not set ThinkingBudget (API returns 400)")
	}

	flashCfg := synth.buildThinkingConfig("gemini-2.5-flash")
	if flashCfg.ThinkingBudget == nil || *flashCfg.ThinkingBudget != -1 {
		t.Error("gemini-2.5 should use dynamic thinkingBudget (-1)")
	}

	flashLiteCfg := synth.buildThinkingConfig("gemini-3.5-flash-lite")
	if flashLiteCfg.ThinkingLevel != genai.ThinkingLevelMinimal {
		t.Errorf("gemini-3.x should use MINIMAL thinking, got %q", flashLiteCfg.ThinkingLevel)
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

// runFallbackScenario spins up a mock Gemini API that fails the primary
// grounded model with failStatus and succeeds for any other model, then runs a
// manual synthesis and returns the ordered list of requested models.
func runFallbackScenario(t *testing.T, failStatus int, failBody string) []string {
	t.Helper()

	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := modelFromPath(r.URL.Path)
		hits = append(hits, model)

		if model == groundedModels[0] {
			w.WriteHeader(failStatus)
			_, _ = io.WriteString(w, failBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"CLASIFICACION: CALMA_REAJUSTE\n\nResumen Ejecutivo: sin cambios significativos."}]},"finishReason":"STOP","index":0}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:     genai.BackendGeminiAPI,
		APIKey:      "test-key",
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("failed to create test client: %v", err)
	}

	synth := NewGemmaSynthesizer("test-key", nil)
	synth.client = client
	synth.retryBackoff = time.Millisecond

	resp, err := synth.Synthesize(ctx, SynthesisRequest{
		TriggerType: "TEST",
		CellID:      "G_1_1",
		IsManual:    true,
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if resp.ModelUsed != groundedModels[1] {
		t.Errorf("expected fallback model %q, got %q", groundedModels[1], resp.ModelUsed)
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
	if hits[0] != groundedModels[0] {
		t.Errorf("first request should target %q, got %q", groundedModels[0], hits[0])
	}
	for _, h := range hits {
		if h == groundedModels[1] {
			return
		}
	}
	t.Errorf("expected a request to the fallback model %q, hits=%v", groundedModels[1], hits)
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
