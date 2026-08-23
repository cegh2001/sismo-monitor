package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

const (
	DefaultModelName = "gemma-4-31b-it"
	CellCooldown     = 10 * time.Minute
	GlobalCooldown   = 10 * time.Second
	MaxDailyReports  = 50
)

// fallbackModels defines the ordered chain of models to try when the primary
// model hits a quota/rate-limit error. Each entry is tried with Search
// Grounding enabled. If all grounded models fail, the primary model is tried
// one last time without Search Grounding as a final fallback.
var fallbackModels = []string{
	"gemma-4-31b-it",       // Primary: Gemma 4 with Search Grounding
	"gemini-2.5-flash",     // Fallback 1: higher RPM (10) and RPD (500), grounding 1500 RPD
	"gemini-2.5-flash-lite", // Fallback 2: highest RPM (15) and RPD (1000), grounding 500 RPD
}

// GemmaSynthesizer orchestrates LLM report generation using Gemma 4 via Google GenAI SDK.
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
}

// NewGemmaSynthesizer initializes a new GemmaSynthesizer.
func NewGemmaSynthesizer(apiKey string, logger func(string, ...interface{})) *GemmaSynthesizer {
	return &GemmaSynthesizer{
		apiKey:       apiKey,
		modelName:    DefaultModelName,
		cellLastSent: make(map[string]time.Time),
		logger:       logger,
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

// Synthesize generates a natural language seismic narrative report.
// It tries a chain of models with Search Grounding when quota is exceeded,
// falling back to the primary model without grounding as a last resort.
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

	promptText, err := BuildPrompt(req)
	if err != nil {
		return SynthesisResponse{}, err
	}

	if g.logger != nil {
		g.logger("Starting synthesis for cell %s (Trigger: %s, PrevAnalyses: %d)", req.CellID, req.TriggerType, len(req.PreviousReports))
	}

	// Try each model in the fallback chain with Search Grounding
	var lastErr error
	for i, modelName := range fallbackModels {
		if g.logger != nil && i > 0 {
			g.logger("Fallback chain: trying model %d/%d: %s", i+1, len(fallbackModels), modelName)
		}

		resp, err := g.callWithSearchGrounding(ctx, client, modelName, promptText)
		if err == nil {
			// Success
			if g.logger != nil {
				if i > 0 {
					g.logger("Fallback succeeded with model: %s", modelName)
				} else {
					g.logger("Primary model %s succeeded with Search Grounding", modelName)
				}
			}
			g.updateRateLimit(req.CellID, now)
			parsedRes := parseResponse(resp, modelName, now)
			g.mu.Lock()
			g.history = append(g.history, parsedRes)
			if len(g.history) > 5 {
				g.history = g.history[len(g.history)-5:]
			}
			g.mu.Unlock()
			return parsedRes, nil
		}

		// Check if it's a quota/rate-limit error
		errStr := err.Error()
		if !strings.Contains(errStr, "RESOURCE_EXHAUSTED") && !strings.Contains(errStr, "429") {
			// Not a quota error, return immediately
			return SynthesisResponse{}, fmt.Errorf("GenerateContent failed: %w", err)
		}

		// Quota error, save for potential final error message
		lastErr = err
		if g.logger != nil {
			g.logger("Model %s hit quota limit, trying next fallback...", modelName)
		}
	}

	// All grounded models failed with quota errors, try primary model without grounding
	if g.logger != nil {
		g.logger("All grounded models exhausted, trying %s without Search Grounding as final fallback", fallbackModels[0])
	}

	resp, err := g.callWithoutSearchGrounding(ctx, client, fallbackModels[0], promptText)
	if err != nil {
		// Final fallback also failed, return the original quota error
		retryMsg := g.extractQuotaErrorMessage(lastErr)
		return SynthesisResponse{}, fmt.Errorf("%s", retryMsg)
	}

	// Final fallback succeeded
	if g.logger != nil {
		g.logger("Final fallback succeeded: %s without Search Grounding", fallbackModels[0])
	}
	g.updateRateLimit(req.CellID, now)
	parsedRes := parseResponse(resp, fallbackModels[0]+" (no grounding)", now)
	g.mu.Lock()
	g.history = append(g.history, parsedRes)
	if len(g.history) > 5 {
		g.history = g.history[len(g.history)-5:]
	}
	g.mu.Unlock()
	return parsedRes, nil
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
func (g *GemmaSynthesizer) callWithSearchGrounding(ctx context.Context, client *genai.Client, modelName, promptText string) (*genai.GenerateContentResponse, error) {
	tools := []*genai.Tool{
		{GoogleSearch: &genai.GoogleSearch{}},
	}
	config := &genai.GenerateContentConfig{
		Tools: tools,
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevel("HIGH"),
		},
	}
	return client.Models.GenerateContent(ctx, modelName, genai.Text(promptText), config)
}

// callWithoutSearchGrounding invokes the model without any tools (fallback when quota is exceeded).
func (g *GemmaSynthesizer) callWithoutSearchGrounding(ctx context.Context, client *genai.Client, modelName, promptText string) (*genai.GenerateContentResponse, error) {
	config := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevel("HIGH"),
		},
	}
	return client.Models.GenerateContent(ctx, modelName, genai.Text(promptText), config)
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
