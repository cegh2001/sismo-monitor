package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"sismo-monitor/internal/alert"
)

var (
	gemmaTitleStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFCC00")).Bold(true)
	gemmaHeaderStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E8E93")).Bold(true)
	gemmaBadgeConfirm    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3B30")).Bold(true) // Red
	gemmaBadgeCalma      = lipgloss.NewStyle().Foreground(lipgloss.Color("#30D158"))            // Green
	gemmaGeneratingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true).Blink(true)
	gemmaIdleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#30D158"))
	gemmaErrorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3B30")).Bold(true)
	gemmaCitationStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#0A84FF"))
	gemmaModelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#BF5AF2"))
	gemmaTimeStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E8E93"))
	gemmaBodyBoxStyle    = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#3A3A3C")).
				Padding(1, 2)
	gemmaFooterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E8E93"))
)

// renderGemmaView builds the dedicated Gemma 4 analysis view.
func (m Model) renderGemmaView() string {
	var s string

	// Header
	s += doubleDivider
	s += gemmaTitleStyle.Render("              ANÁLISIS SISMOLÓGICO — GEMMA 4 (GOOGLE SEARCH GROUNDED)") + "\n"
	s += doubleDivider

	// Status line
	statusIcon := "✅"
	statusText := "Inactivo — presione [g] para solicitar un nuevo análisis sismológico contextualizado."
	statusStyle := gemmaIdleStyle

	if m.gemmaError != "" {
		statusIcon = "❌"
		statusText = m.gemmaError
		statusStyle = gemmaErrorStyle
	} else if m.gemmaGenerating {
		statusIcon = "🤖"
		statusText = "Generando análisis sismológico con Gemma 4 (Google Search Grounding)... Por favor espere."
		statusStyle = gemmaGeneratingStyle
	} else if len(m.GemmaReports) > 0 {
		last := m.GemmaReports[len(m.GemmaReports)-1]
		statusText = fmt.Sprintf("Último análisis: %s — %s (%s)",
			last.GeneratedAt.Format("15:04:05"),
			last.ReportType,
			last.ModelUsed)
	}

	s += fmt.Sprintf("  %s  %s\n", statusIcon, statusStyle.Render(statusText))
	s += singleDivider

	// Latest report (full detail)
	if len(m.GemmaReports) > 0 {
		rep := m.GemmaReports[len(m.GemmaReports)-1]

		badge := gemmaBadgeCalma.Render("[CALMA_REAJUSTE]")
		if rep.ReportType == alert.ReportConfirmacion {
			badge = gemmaBadgeConfirm.Render("[CONFIRMACION]")
		}

		s += fmt.Sprintf("  %s  %s — %s\n",
			badge,
			gemmaTimeStyle.Render(rep.GeneratedAt.Format("2006-01-02 15:04:05")),
			gemmaModelStyle.Render(rep.ModelUsed))

		if rep.Summary != "" {
			s += fmt.Sprintf("  %s\n", gemmaHeaderStyle.Render("Resumen:")+" "+rep.Summary)
		}

		s += "\n"
		// Full body in a bordered box
		wrapWidth := m.termWidth - 8
		if wrapWidth < 50 {
			wrapWidth = 50
		}
		wrappedBody := lipgloss.NewStyle().Width(wrapWidth).Render(rep.Body)
		s += gemmaBodyBoxStyle.Width(m.termWidth - 4).Render(wrappedBody)
		s += "\n"

		// Citations
		if len(rep.Citations) > 0 {
			s += fmt.Sprintf("  %s\n", gemmaHeaderStyle.Render("Fuentes verificadas (Google Search Grounding):"))
			for _, c := range rep.Citations {
				s += fmt.Sprintf("  • %s\n", gemmaCitationStyle.Render(fmt.Sprintf("%s → %s", c.Title, c.URL)))
			}
			s += "\n"
		}
	} else {
		s += "  Aún no se ha generado ningún reporte. Presione [g] para iniciar el primer análisis.\n\n"
	}

	s += singleDivider

	// Bitácora (history)
	s += fmt.Sprintf("  %s  (%d reportes)\n", gemmaHeaderStyle.Render("BITÁCORA DE ANÁLISIS"), len(m.GemmaReports))
	if len(m.GemmaReports) <= 1 {
		s += "  (Sin historial previo)\n"
	} else {
		// Show all except the latest (already shown above)
		history := m.GemmaReports[:len(m.GemmaReports)-1]
		// Apply scroll
		start := len(history) - 1 - m.gemmaScroll
		end := start - 5
		if end < -1 {
			end = -1
		}
		displayed := 0
		for i := start; i > end; i-- {
			if i < 0 {
				break
			}
			rep := history[i]
			badge := gemmaBadgeCalma.Render("[CALMA]")
			if rep.ReportType == alert.ReportConfirmacion {
				badge = gemmaBadgeConfirm.Render("[CONF]")
			}
			s += fmt.Sprintf("  %s  %s — %s\n",
				badge,
				gemmaTimeStyle.Render(rep.GeneratedAt.Format("2006-01-02 15:04:05")),
				truncate(rep.Summary, 80))
			displayed++
		}
		if displayed == 0 {
			s += "  (Sin historial previo)\n"
		}
	}

	s += singleDivider

	// Footer with key bindings
	s += gemmaFooterStyle.Render("  [g] Solicitar análisis  │  [d] Dashboard  │  [p] Proyecciones  │  [↑/↓] Scroll bitácora  │  [q] Salir") + "\n"
	s += doubleDivider

	return s
}
