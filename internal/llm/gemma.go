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
	modelName := g.modelName
	if len(req.PreviousReports) == 0 && len(g.history) > 0 {
		req.PreviousReports = make([]SynthesisResponse, len(g.history))
		copy(req.PreviousReports, g.history)
	}
	g.mu.Unlock()

	promptText, err := BuildPrompt(req)
	if err != nil {
		return SynthesisResponse{}, err
	}

	tools := []*genai.Tool{
		{GoogleSearch: &genai.GoogleSearch{}},
	}

	config := &genai.GenerateContentConfig{
		Tools: tools,
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevel("HIGH"),
		},
	}

	if g.logger != nil {
		g.logger("Invoking Gemma 4 (%s) for cell %s (Trigger: %s, PrevAnalyses: %d)", modelName, req.CellID, req.TriggerType, len(req.PreviousReports))
	}

	resp, err := client.Models.GenerateContent(ctx, modelName, genai.Text(promptText), config)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "RESOURCE_EXHAUSTED") || strings.Contains(errStr, "429") {
			retryMsg := "⏳ Límite de cuota API excedido (Google GenAI Rate Limit 429)."
			if idx := strings.Index(errStr, "Please retry in "); idx != -1 {
				rest := errStr[idx+len("Please retry in "):]
				if endIdx := strings.IndexAny(rest, ".,\n"); endIdx != -1 {
					retryMsg = fmt.Sprintf("⏳ Cuota de Google GenAI superada (RPM). Espere %s antes de solicitar otro análisis.", rest[:endIdx])
				}
			}
			return SynthesisResponse{}, fmt.Errorf("%s", retryMsg)
		}
		return SynthesisResponse{}, fmt.Errorf("GenerateContent failed: %w", err)
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
