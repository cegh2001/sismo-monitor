package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

const (
	DefaultModelName = "gemini-2.5-flash"
	CellCooldown     = 10 * time.Minute
	GlobalCooldown   = 10 * time.Second
	MaxDailyReports  = 50

	// CallTimeout bounds a single model request so a slow or hung call cannot
	// block the analysis indefinitely.
	CallTimeout = 60 * time.Second
	// MaxOutputTokens caps the narrative length and prevents runaway output.
	MaxOutputTokens = 8192
)

const (
	EngineGemini = "gemini"
	EngineGemma  = "gemma"
)

type modelStep struct {
	Name     string
	Grounded bool
}

// geminiCascade is the primary cascade of modern free-tier Gemini models.
// Verified against v1beta/models: gemini-2.5-flash is the only free-tier model
// supporting Google Search Grounding; Gemini 3.x models work fast and reliably
// without Search Grounding when ThinkingLevel is MINIMAL.
var geminiCascade = []modelStep{
	{Name: "gemini-2.5-flash", Grounded: true},
	{Name: "gemini-3.5-flash", Grounded: false},
	{Name: "gemini-3-flash-preview", Grounded: false},
	{Name: "gemini-3.5-flash-lite", Grounded: false},
	{Name: "gemini-3.1-flash-lite", Grounded: false},
}

// gemmaCascade is the Gemma 4 cascade, used both when explicitly selected in
// the TUI and as the final reserve after geminiCascade.
var gemmaCascade = []modelStep{
	{Name: "gemma-4-31b-it", Grounded: true},
	{Name: "gemma-4-26b-a4b-it", Grounded: true},
	{Name: "gemma-4-31b-it", Grounded: false},
	{Name: "gemma-4-26b-a4b-it", Grounded: false},
}

func normalizeEngine(engine string) string {
	if strings.EqualFold(strings.TrimSpace(engine), EngineGemma) {
		return EngineGemma
	}
	return EngineGemini
}

func stepsForEngine(engine string) []modelStep {
	if normalizeEngine(engine) == EngineGemma {
		steps := make([]modelStep, len(gemmaCascade))
		copy(steps, gemmaCascade)
		return steps
	}
	steps := make([]modelStep, 0, len(geminiCascade)+len(gemmaCascade))
	steps = append(steps, geminiCascade...)
	steps = append(steps, gemmaCascade...)
	return steps
}

// GemmaSynthesizer orchestrates LLM report generation using Gemini and Gemma 4 cascades via Google GenAI SDK.
type GemmaSynthesizer struct {
	mu             sync.Mutex
	apiKey         string
	modelName      string
	client         *genai.Client
	cellLastSent   map[string]time.Time
	globalLastSent time.Time
	dailyCount     int
	lastResetDay   int
	history        []SynthesisResponse
	logger         func(string, ...interface{})
	// retryBackoff is the base delay for transient-error retries. Tests lower
	// it to keep suites fast.
	retryBackoff time.Duration
}

// NewGemmaSynthesizer initializes a new GemmaSynthesizer.
func NewGemmaSynthesizer(apiKey string, logger func(string, ...interface{})) *GemmaSynthesizer {
	return &GemmaSynthesizer{
		apiKey:       apiKey,
		modelName:    DefaultModelName,
		cellLastSent: make(map[string]time.Time),
		logger:       logger,
		retryBackoff: time.Second,
	}
}

// SetModelName overrides the default model name (useful for testing or fallback).
func (g *GemmaSynthesizer) SetModelName(model string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.modelName = model
}

// initClientLocked ensures the genai Client is created.
func (g *GemmaSynthesizer) initClientLocked(ctx context.Context) error {
	if g.client != nil {
		return nil
	}
	if g.apiKey == "" {
		return fmt.Errorf("gemini API key is empty")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  g.apiKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create genai client: %w", err)
	}

	g.client = client
	return nil
}

// checkRateLimit checks cell cooldown, global cooldown, and daily limits.
func (g *GemmaSynthesizer) checkRateLimit(cellID string, isManual bool, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Reset daily counter if a new calendar day has started
	currentDay := now.YearDay()
	if currentDay != g.lastResetDay {
		g.dailyCount = 0
		g.lastResetDay = currentDay
	}

	if g.dailyCount >= MaxDailyReports {
		return fmt.Errorf("daily report limit reached (%d/%d)", g.dailyCount, MaxDailyReports)
	}

	if !isManual {
		if last, found := g.cellLastSent[cellID]; found {
			if now.Sub(last) < CellCooldown {
				return fmt.Errorf("cell %s in cooldown (remaining: %v)", cellID, CellCooldown-now.Sub(last))
			}
		}
	}

	if now.Sub(g.globalLastSent) < GlobalCooldown {
		return fmt.Errorf("global cooldown active (remaining: %v)", GlobalCooldown-now.Sub(g.globalLastSent))
	}

	return nil
}

// updateRateLimit registers a successful generation attempt.
func (g *GemmaSynthesizer) updateRateLimit(cellID string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.cellLastSent[cellID] = now
	g.globalLastSent = now
	g.dailyCount++
}

// isTransientError returns true if the error is transient and should be retried.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// 500: Internal Server Error
	// 502: Bad Gateway
	// 503: Service Unavailable / Timeout
	// 504: Gateway Timeout
	return strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "timed out") ||
		strings.Contains(errStr, "timeout")
}

// callWithRetry wraps a model call with exponential backoff retry for transient errors.
func (g *GemmaSynthesizer) callWithRetry(ctx context.Context, callFn func() (*genai.GenerateContentResponse, error), modelName string) (*genai.GenerateContentResponse, error) {
	const maxRetries = 3
	backoff := g.retryBackoff
	if backoff <= 0 {
		backoff = time.Second
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := callFn()
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Only retry on transient errors
		if !isTransientError(err) {
			return nil, err
		}

		// Don't wait after the last attempt
		if attempt < maxRetries {
			if g.logger != nil {
				g.logger("Model %s: transient error (attempt %d/%d), retrying in %v...", modelName, attempt+1, maxRetries, backoff)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2 // Exponential backoff: 1s, 2s, 4s
		}
	}

	// All retries exhausted. Keep the original error (e.g. the 503) so the
	// caller can classify it and fall back instead of losing the cause.
	return nil, fmt.Errorf("all %d retry attempts failed: %w", maxRetries, lastErr)
}

// Synthesize generates a natural language seismic narrative report.
//
// Depending on req.Engine ("gemini" default or "gemma"), it walks the ordered
// model cascade and falls back to the next step on ANY non-fatal error (quota,
// 5xx, timeouts, validation, empty/blocked responses). In "gemini" mode, the
// Gemma 4 cascade serves as the final reserve if all Gemini models fail.
func (g *GemmaSynthesizer) Synthesize(ctx context.Context, req SynthesisRequest) (SynthesisResponse, error) {
	now := time.Now()

	if err := g.checkRateLimit(req.CellID, req.IsManual, now); err != nil {
		return SynthesisResponse{}, fmt.Errorf("rate limit: %w", err)
	}

	g.mu.Lock()
	if err := g.initClientLocked(ctx); err != nil {
		g.mu.Unlock()
		return SynthesisResponse{}, err
	}
	client := g.client
	if len(req.PreviousReports) == 0 && len(g.history) > 0 {
		req.PreviousReports = make([]SynthesisResponse, len(g.history))
		copy(req.PreviousReports, g.history)
	}
	g.mu.Unlock()

	engine := normalizeEngine(req.Engine)
	req.Engine = engine

	promptText, err := BuildPrompt(req)
	if err != nil {
		return SynthesisResponse{}, err
	}

	steps := stepsForEngine(engine)
	if g.logger != nil {
		g.logger("Starting synthesis for cell %s (Engine: %s, Trigger: %s, PrevAnalyses: %d)", req.CellID, engine, req.TriggerType, len(req.PreviousReports))
	}

	var firstQuotaErr error
	var lastErr error
	for i, step := range steps {
		mode := "no grounding"
		if step.Grounded {
			mode = "grounded"
		}
		if g.logger != nil && i > 0 {
			g.logger("Fallback cascade: trying step %d/%d: %s (%s)", i+1, len(steps), step.Name, mode)
		}

		parsed, err := g.attemptModel(ctx, client, step.Name, promptText, step.Grounded)
		if err == nil {
			if g.logger != nil {
				if i > 0 {
					g.logger("Fallback succeeded at step %d/%d: %s (%s)", i+1, len(steps), step.Name, mode)
				} else {
					g.logger("Primary model %s (%s) succeeded", step.Name, mode)
				}
			}
			return g.complete(req, parsed, now), nil
		}

		lastErr = err
		if firstQuotaErr == nil && (strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") || strings.Contains(err.Error(), "429")) {
			firstQuotaErr = err
		}
		if isFatalError(err) {
			return SynthesisResponse{}, fmt.Errorf("GenerateContent failed: %w", err)
		}
		if g.logger != nil {
			g.logger("Model %s (%s) failed: %v. Trying next model...", step.Name, mode, err)
		}
	}

	if firstQuotaErr != nil {
		return SynthesisResponse{}, fmt.Errorf("%s", g.extractQuotaErrorMessage(firstQuotaErr))
	}
	return SynthesisResponse{}, fmt.Errorf("GenerateContent failed: %w", lastErr)
}

// attemptModel performs one model call (with transient-error retries), validates
// that the response carries usable text, and parses it into a report.
func (g *GemmaSynthesizer) attemptModel(ctx context.Context, client *genai.Client, modelName, promptText string, grounded bool) (SynthesisResponse, error) {
	label := modelName
	if !grounded {
		label = modelName + " (no grounding)"
	}

	resp, err := g.callWithRetry(ctx, func() (*genai.GenerateContentResponse, error) {
		if grounded {
			return g.callWithSearchGrounding(ctx, client, modelName, promptText)
		}
		return g.callWithoutSearchGrounding(ctx, client, modelName, promptText)
	}, label)
	if err != nil {
		return SynthesisResponse{}, err
	}

	if !responseIsUsable(resp) {
		return SynthesisResponse{}, fmt.Errorf("model %s returned an unusable response (finishReason=%s)", label, finishReasonOf(resp))
	}

	return parseResponse(resp, label, time.Now()), nil
}

// complete registers a successful generation against the rate limits and appends
// it to the rolling session history.
func (g *GemmaSynthesizer) complete(req SynthesisRequest, parsed SynthesisResponse, now time.Time) SynthesisResponse {
	g.updateRateLimit(req.CellID, now)
	g.mu.Lock()
	g.history = append(g.history, parsed)
	if len(g.history) > 5 {
		g.history = g.history[len(g.history)-5:]
	}
	g.mu.Unlock()
	return parsed
}

// responseIsUsable reports whether a response contains at least one non-thought
// text part. Empty or safety/recitation-blocked responses are treated as
// failures so the caller can fall back to the next model.
func responseIsUsable(resp *genai.GenerateContentResponse) bool {
	if resp == nil || len(resp.Candidates) == 0 {
		return false
	}
	cand := resp.Candidates[0]
	if cand.Content == nil {
		return false
	}
	for _, part := range cand.Content.Parts {
		if part != nil && !part.Thought && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

// finishReasonOf returns the finish reason of the first candidate, or "none".
func finishReasonOf(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return "none"
	}
	return string(resp.Candidates[0].FinishReason)
}

// isFatalError reports whether the chain should stop instead of trying the next
// model. Auth/permission errors and a cancelled parent context are fatal;
// everything else (quota, 5xx, timeouts, 400 validation, unusable responses)
// falls through to the next model.
func isFatalError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	errStr := err.Error()
	for _, marker := range []string{"401", "403", "UNAUTHENTICATED", "PERMISSION_DENIED", "API key not valid", "API_KEY_INVALID"} {
		if strings.Contains(errStr, marker) {
			return true
		}
	}
	return false
}

// extractQuotaErrorMessage parses a 429/RESOURCE_EXHAUSTED error and returns
// a user-friendly message with the retry time if available.
func (g *GemmaSynthesizer) extractQuotaErrorMessage(err error) string {
	if err == nil {
		return "⏳ Límite de cuota API excedido (Google GenAI Rate Limit 429)."
	}
	errStr := err.Error()
	retryMsg := "⏳ Límite de cuota API excedido (Google GenAI Rate Limit 429)."
	if idx := strings.Index(errStr, "Please retry in "); idx != -1 {
		rest := errStr[idx+len("Please retry in "):]
		// Capture until first newline or period, preserving the full time unit
		if endIdx := strings.IndexAny(rest, "\n"); endIdx != -1 {
			retryText := strings.TrimRight(rest[:endIdx], ".,")
			retryMsg = fmt.Sprintf("⏳ Cuota de Google GenAI superada (RPM). Espere %s antes de solicitar otro análisis.", retryText)
		} else if endIdx := strings.Index(rest, "."); endIdx != -1 {
			retryText := strings.TrimSpace(rest[:endIdx])
			retryMsg = fmt.Sprintf("⏳ Cuota de Google GenAI superada (RPM). Espere %s antes de solicitar otro análisis.", retryText)
		}
	}
	return retryMsg
}

// callWithSearchGrounding invokes the model with GoogleSearch tool enabled.
// Uses the appropriate thinking configuration based on model family:
// - Gemini 2.5 series: thinkingBudget (token count)
// - Gemini 3.x and Gemma: thinkingLevel (semantic levels)
func (g *GemmaSynthesizer) callWithSearchGrounding(ctx context.Context, client *genai.Client, modelName, promptText string) (*genai.GenerateContentResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	tools := []*genai.Tool{
		{GoogleSearch: &genai.GoogleSearch{}},
	}
	config := &genai.GenerateContentConfig{
		Tools:           tools,
		ThinkingConfig:  g.buildThinkingConfig(modelName),
		MaxOutputTokens: MaxOutputTokens,
	}
	return client.Models.GenerateContent(callCtx, modelName, genai.Text(promptText), config)
}

// callWithoutSearchGrounding invokes the model without any tools (final fallback).
func (g *GemmaSynthesizer) callWithoutSearchGrounding(ctx context.Context, client *genai.Client, modelName, promptText string) (*genai.GenerateContentResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	config := &genai.GenerateContentConfig{
		ThinkingConfig:  g.buildThinkingConfig(modelName),
		MaxOutputTokens: MaxOutputTokens,
	}
	return client.Models.GenerateContent(callCtx, modelName, genai.Text(promptText), config)
}

// buildThinkingConfig returns the appropriate thinking configuration for the given model.
// Gemini 2.5 series uses thinkingBudget (token count); Gemini 3.x and Gemma use thinkingLevel.
func (g *GemmaSynthesizer) buildThinkingConfig(modelName string) *genai.ThinkingConfig {
	// Gemini 2.5 series: use thinkingBudget
	if strings.Contains(modelName, "gemini-2.5") {
		return &genai.ThinkingConfig{
			ThinkingBudget: genai.Ptr(int32(-1)), // Dynamic thinking budget
		}
	}
	// Gemma 4 only accepts MINIMAL or HIGH (LOW, MEDIUM and thinkingBudget
	// return 400). MINIMAL keeps latency ~22s instead of ~91s.
	if strings.HasPrefix(modelName, "gemma") {
		return &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevelMinimal,
		}
	}
	// Gemini 3.x: semantic thinking level. MINIMAL keeps the last-resort
	// response fast.
	return &genai.ThinkingConfig{
		ThinkingLevel: genai.ThinkingLevelMinimal,
	}
}

// parseResponse extracts the narrative body and Search Grounding citations from genai output.
func parseResponse(resp *genai.GenerateContentResponse, modelName string, now time.Time) SynthesisResponse {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return SynthesisResponse{
			ReportType:  ReportCalmaReajuste,
			Summary:     "Reporte no disponible.",
			Body:        "No se pudo obtener respuesta del sintetizador.",
			ModelUsed:   modelName,
			GeneratedAt: now,
		}
	}

	cand := resp.Candidates[0]
	var bodyBuilder strings.Builder
	var citations []Citation

	for _, part := range cand.Content.Parts {
		if part.Thought {
			continue // Skip model's internal thinking process
		}
		if part.Text != "" {
			bodyBuilder.WriteString(part.Text)
		}
	}

	// Extract Grounding metadata citations if available
	if cand.GroundingMetadata != nil {
		for _, chunk := range cand.GroundingMetadata.GroundingChunks {
			if chunk.Web != nil {
				citations = append(citations, Citation{
					Title: chunk.Web.Title,
					URL:   chunk.Web.URI,
				})
			}
		}
	}

	fullText := bodyBuilder.String()

	// Strip <thought>...</thought> tags if present in text
	if idx := strings.Index(fullText, "</thought>"); idx != -1 {
		fullText = strings.TrimSpace(fullText[idx+len("</thought>"):])
	}

	// Sanitize leading draft/thinking text by searching for standard report headers
	reportHeaders := []string{"CLASIFICACION:", "**Resumen Ejecutivo:**", "Resumen Ejecutivo:", "CONFIRMACION", "CALMA_REAJUSTE"}
	for _, h := range reportHeaders {
		if idx := strings.Index(fullText, h); idx != -1 {
			fullText = strings.TrimSpace(fullText[idx:])
			break
		}
	}

	// Sanitize LaTeX math markup that distorts TUI rendering
	fullText = sanitizeLLMText(fullText)

	reportType := ReportCalmaReajuste
	upperText := strings.ToUpper(fullText)
	if strings.Contains(upperText, "CONFIRMACION") && !strings.HasPrefix(upperText, "CLASIFICACION: CALMA_REAJUSTE") {
		reportType = ReportConfirmacion
	}

	// Extract first 2 clean lines for summary
	lines := strings.Split(strings.TrimSpace(fullText), "\n")
	summary := fullText
	if len(lines) > 0 {
		summary = lines[0]
		if len(lines) > 1 && len(summary) < 80 {
			summary = summary + " " + lines[1]
		}
	}
	summary = strings.TrimPrefix(summary, "CLASIFICACION: CONFIRMACION")
	summary = strings.TrimPrefix(summary, "CLASIFICACION: CALMA_REAJUSTE")
	summary = strings.TrimPrefix(summary, "**Resumen Ejecutivo:**")
	summary = strings.TrimPrefix(summary, "Resumen Ejecutivo:")
	summary = strings.TrimSpace(summary)

	if len(summary) > 150 {
		summary = summary[:147] + "..."
	}

	return SynthesisResponse{
		ReportType:  reportType,
		Summary:     summary,
		Body:        fullText,
		Citations:   citations,
		ModelUsed:   modelName,
		GeneratedAt: now,
	}
}

// sanitizeLLMText replaces LaTeX formulas and math syntax with clean text & Unicode.
func sanitizeLLMText(text string) string {
	r := strings.NewReplacer(
		"$\\rightarrow$", "->",
		"\\rightarrow", "->",
		"$\\approx$", "≈",
		"\\approx", "≈",
		"$b$-value", "valor-b",
		"$b$", "valor-b",
		"$M_w$", "Mw",
		"$M$", "M",
		"\\sim", "~",
		"$$", "",
		"$", "",
	)
	return r.Replace(text)
}
