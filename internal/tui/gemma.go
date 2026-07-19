package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"sismo-monitor/internal/alert"
)

var (
	// OpenCode / Gemini CLI Design System Styles
	gcTagStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#7C3AED")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 1)

	gcOnlineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E")).
			Bold(true)

	gcGeneratingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EAB308")).
			Bold(true)

	gcErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	gcModelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C084FC")).
			Bold(true)

	gcGroundedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#38BDF8")).
			Bold(true)

	gcBadgeConfirm = lipgloss.NewStyle().
			Background(lipgloss.Color("#991B1B")).
			Foreground(lipgloss.Color("#FEF2F2")).
			Bold(true).
			Padding(0, 1)

	gcBadgeCalma = lipgloss.NewStyle().
			Background(lipgloss.Color("#166534")).
			Foreground(lipgloss.Color("#F0FDF4")).
			Bold(true).
			Padding(0, 1)

	gcSummaryBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("#A855F7")).
				PaddingLeft(1).
				Foreground(lipgloss.Color("#E2E8F0"))

	gcReportBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#7C3AED")).
				Padding(1, 1)

	gcCitationTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			Bold(true)

	gcCitationLink = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#38BDF8")).
			Underline(true)

	gcFooterStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF")).
			Background(lipgloss.Color("#18181B")).
			Padding(0, 1)

	gcTabActive = lipgloss.NewStyle().
			Background(lipgloss.Color("#6D28D9")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 1)

	gcTabInactive = lipgloss.NewStyle().
			Background(lipgloss.Color("#27272A")).
			Foreground(lipgloss.Color("#A1A1AA")).
			Padding(0, 1)
)

// renderGemmaView builds the modernized Gemma 4 analysis view with OpenCode / Gemini CLI UX.
func (m Model) renderGemmaView() string {
	var s string

	// Header Banner (OpenCode / Gemini CLI aesthetic)
	tag := gcTagStyle.Render("🤖 GEMMA 4")

	var statusPill string
	if m.gemmaError != "" {
		statusPill = gcErrorStyle.Render("✖ ERROR")
	} else if m.gemmaGenerating {
		frame := gemmaSpinnerFrames[m.gemmaSpinnerFrame%len(gemmaSpinnerFrames)]
		statusPill = gcGeneratingStyle.Render(fmt.Sprintf("%s GENERANDO ANALISIS...", frame))
	} else {
		statusPill = gcOnlineStyle.Render("● ONLINE")
	}

	modelPill := gcModelStyle.Render("gemma-4-31b-it")
	groundedPill := gcGroundedStyle.Render("[GOOGLE SEARCH GROUNDED]")
	reportsPill := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")).Render(fmt.Sprintf("Reportes: %d", len(m.GemmaReports)))

	s += doubleDivider
	s += fmt.Sprintf("  %s  %s  │  Modelo: %s  │  %s  │  %s\n", tag, statusPill, modelPill, groundedPill, reportsPill)
	s += doubleDivider

	// Error or Active Status Message Banner
	if m.gemmaError != "" {
		s += fmt.Sprintf("  %s\n", gcErrorStyle.Render(m.gemmaError))
		s += singleDivider
	} else if m.gemmaGenerating {
		frame := gemmaSpinnerFrames[m.gemmaSpinnerFrame%len(gemmaSpinnerFrames)]
		s += fmt.Sprintf("  %s %s\n",
			gcGeneratingStyle.Render(frame),
			gcGeneratingStyle.Render("Sintetizando informe sismológico contextualizado con Gemma 4 y Google Search Grounding..."))
		s += singleDivider
	}

	// Bitácora / Report History Tabs Selector
	numReports := len(m.GemmaReports)
	if numReports > 0 {
		// Clamp selection
		selIdx := m.gemmaSelectedReport
		if selIdx < 0 {
			selIdx = 0
		}
		if selIdx >= numReports {
			selIdx = numReports - 1
		}

		s += "  BITÁCORA DE REPORTES: "
		for i, rep := range m.GemmaReports {
			tStr := rep.GeneratedAt.Format("15:04:05")
			badgeStr := "CALMA"
			if rep.ReportType == alert.ReportConfirmacion {
				badgeStr = "CONFIRMADO"
			}
			label := fmt.Sprintf("#%d %s (%s)", i+1, tStr, badgeStr)

			if i == selIdx {
				s += gcTabActive.Render("★ "+label) + " "
			} else {
				s += gcTabInactive.Render(label) + " "
			}
		}
		s += "\n"
		s += singleDivider
	}

	// Main Report Body & Details
	if numReports > 0 {
		selIdx := m.gemmaSelectedReport
		if selIdx < 0 {
			selIdx = 0
		}
		if selIdx >= numReports {
			selIdx = numReports - 1
		}
		rep := m.GemmaReports[selIdx]

		badge := gcBadgeCalma.Render("✅ [CALMA Y REAJUSTE CORTICAL]")
		if rep.ReportType == alert.ReportConfirmacion {
			badge = gcBadgeConfirm.Render("⚠️ [CONFIRMACIÓN DE RIESGO SÍSMICO]")
		}

		s += fmt.Sprintf("  %s  %s  —  %s\n",
			badge,
			lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")).Render(rep.GeneratedAt.Format("2006-01-02 15:04:05")),
			gcModelStyle.Render(rep.ModelUsed))

		if rep.Summary != "" {
			s += "\n  " + gcSummaryBoxStyle.Render("Resumen Ejecutivo: "+rep.Summary) + "\n"
		}

		// Format text into terminal markdown
		bodyWidth := m.termWidth - 8
		if bodyWidth < 40 {
			bodyWidth = 40
		}
		formattedLines := formatTerminalMarkdown(rep.Body, bodyWidth)

		// Calculate viewport bounds
		viewportHeight := m.termHeight - 15
		if viewportHeight < 6 {
			viewportHeight = 6
		}

		totalLines := len(formattedLines)
		maxScroll := totalLines - viewportHeight
		if maxScroll < 0 {
			maxScroll = 0
		}

		scroll := m.gemmaBodyScroll
		if scroll > maxScroll {
			scroll = maxScroll
		}
		if scroll < 0 {
			scroll = 0
		}

		endLine := scroll + viewportHeight
		if endLine > totalLines {
			endLine = totalLines
		}

		visibleLines := formattedLines[scroll:endLine]
		bodyContent := strings.Join(visibleLines, "\n")

		percent := 100
		if totalLines > 0 {
			percent = (endLine * 100) / totalLines
		}
		scrollIndicator := fmt.Sprintf(" ANÁLISIS DETALLADO (Líneas %d-%d/%d | %d%%) ", scroll+1, endLine, totalLines, percent)
		if scroll > 0 {
			scrollIndicator += "▲ "
		}
		if endLine < totalLines {
			scrollIndicator += "▼ "
		}

		s += fmt.Sprintf("\n  %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#A855F7")).Bold(true).Render(scrollIndicator))
		s += gcReportBoxStyle.Width(m.termWidth - 4).Render(bodyContent) + "\n"

		// Grounding Citations
		if len(rep.Citations) > 0 {
			s += "\n  " + gcCitationTitle.Render("🔍 FUENTES VERIFICADAS (Google Search Grounding):") + "\n"
			for _, c := range rep.Citations {
				s += fmt.Sprintf("    • %s ➔ %s\n",
					lipgloss.NewStyle().Foreground(lipgloss.Color("#F8FAFC")).Render(c.Title),
					gcCitationLink.Render(c.URL))
			}
		}
	} else {
		s += "\n  Aún no se ha generado ningún reporte. Presione [g] para solicitar el primer análisis sismológico.\n\n"
	}

	s += singleDivider
	footerText := fmt.Sprintf(" [g] Solicitar Análisis  │  [←/→/Tab] Seleccionar Reporte (%d/%d)  │  [↑/↓/PgUp/PgDn] Scroll Cuerpo  │  [d] Dashboard  │  [q] Salir",
		m.gemmaSelectedReport+1, numReports)
	s += gcFooterStyle.Render(footerText) + "\n"
	s += doubleDivider

	return s
}

// formatTerminalMarkdown converts a markdown narrative string into styled terminal lines.
func formatTerminalMarkdown(body string, width int) []string {
	if width < 40 {
		width = 40
	}
	lines := strings.Split(body, "\n")
	var result []string

	h1Style := lipgloss.NewStyle().Foreground(lipgloss.Color("#A855F7")).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(lipgloss.Color("#38BDF8")).Bold(true)
	boldStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F8FAFC")).Bold(true)
	bulletStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true)
	normStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#D1D5DB"))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			result = append(result, "")
			continue
		}

		// Headers
		if strings.HasPrefix(trimmed, "# ") {
			text := strings.TrimPrefix(trimmed, "# ")
			result = append(result, "", h1Style.Render("▌ "+strings.ToUpper(text)), "")
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			text := strings.TrimPrefix(trimmed, "## ")
			result = append(result, h2Style.Render("◆ "+text))
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			text := strings.TrimPrefix(trimmed, "### ")
			result = append(result, h2Style.Render("  ▸ "+text))
			continue
		}

		// Bullet points (- or *)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			content := trimmed[2:]
			content = processBoldText(content, boldStyle, normStyle)
			wrapped := wrapTerminalLine("  "+bulletStyle.Render("•")+" "+content, width)
			result = append(result, wrapped...)
			continue
		}

		// Numbered lists (1. , 2. )
		if len(trimmed) > 3 && trimmed[0] >= '0' && trimmed[0] <= '9' && trimmed[1] == '.' {
			parts := strings.SplitN(trimmed, " ", 2)
			if len(parts) == 2 {
				numStr := boldStyle.Render(parts[0])
				content := processBoldText(parts[1], boldStyle, normStyle)
				wrapped := wrapTerminalLine("  "+numStr+" "+content, width)
				result = append(result, wrapped...)
				continue
			}
		}

		// Standard paragraph line
		processed := processBoldText(trimmed, boldStyle, normStyle)
		wrapped := wrapTerminalLine("  "+processed, width)
		result = append(result, wrapped...)
	}

	return result
}

// processBoldText replaces **text** with bold styled text.
func processBoldText(s string, boldStyle, normStyle lipgloss.Style) string {
	parts := strings.Split(s, "**")
	if len(parts) == 1 {
		return normStyle.Render(s)
	}
	var out string
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i%2 == 1 {
			out += boldStyle.Render(p)
		} else {
			out += normStyle.Render(p)
		}
	}
	return out
}

// wrapTerminalLine wraps formatted text to the target terminal width.
func wrapTerminalLine(text string, width int) []string {
	rendered := lipgloss.NewStyle().Width(width).Render(text)
	return strings.Split(rendered, "\n")
}
