package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"sismo-monitor/internal/alert"
)

// Predictive Monitor Premium Styles
var (
	titleStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFCC00")).Bold(true)
	headerStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E8E93")).Bold(true)
	faultNameStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#0A84FF")).Bold(true)
	cellStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E5EA"))
	stressHighStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3B30")).Bold(true) // Red
	stressNormalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#30D158"))          // Green
	stressLowStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#0A84FF"))            // Blue
	stressNDStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E8E93"))            // Gray
	bathMagStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9F0A")).Bold(true) // Orange
	omoriRateStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#BF5AF2"))            // Purple
	legendBoxStyle   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3A3A3C")).
			Padding(1, 2).
			Width(95)

	// Phase badge styles — colors are the spec-mandated hex codes (§3).
	// Each style is a small text-only badge rendered as a 9-char fixed-width cell.
	phaseRedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true)
	phaseOrangeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Bold(true)
	phaseYellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00")).Bold(true)
	phaseGrayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	phaseGreenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))

	// ORANGE-decayed style: same color, dimmed/faint per spec §1.7.
	phaseOrangeDecayedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Faint(true)
)

// renderPhaseBadge returns a 9-char phase label colored per spec §3.
// When phase is PhaseReplicas and Decayed is true, the badge uses the
// dimmed variant per spec §1.7.
func renderPhaseBadge(p alert.CellPhase) string {
	switch p.Phase {
	case alert.PhasePrecursor:
		return phaseRedStyle.Render("[RED]    ")
	case alert.PhaseReplicas:
		if p.Decayed {
			return phaseOrangeDecayedStyle.Render("[ORANGE] ")
		}
		return phaseOrangeStyle.Render("[ORANGE] ")
	case alert.PhaseAtencion:
		return phaseYellowStyle.Render("[YELLOW] ")
	case alert.PhaseSilencio:
		return phaseGrayStyle.Render("[GRAY]   ")
	case alert.PhaseEstable:
		return phaseGreenStyle.Render("[GREEN]  ")
	default:
		return phaseGrayStyle.Render("[N/A]    ")
	}
}

// phaseByCell returns the phase for a given grid cell, or PhaseEstable if
// no entry exists (e.g., for a projection cell that has no gap data).
func (m Model) phaseByCell(cell string) alert.CellPhase {
	for _, p := range m.GapState {
		if p.GridCell == cell {
			return p
		}
	}
	return alert.CellPhase{GridCell: cell, Phase: alert.PhaseEstable}
}

// hasNonGrayPhase reports whether any cell in GapState has a non-GRAY phase.
// Used to decide whether to hide GRAY rows in the predictive view (spec §3:
// "GRAY cells MUST be hidden when any non-GRAY cell exists in the snapshot").
func (m Model) hasNonGrayPhase() bool {
	for _, p := range m.GapState {
		if p.Phase != alert.PhaseSilencio {
			return true
		}
	}
	return false
}

func (m Model) renderPredictiveView() string {
	var s string
	s += doubleDivider
	s += titleStyle.Render("                            PROYECCIONES SISMOLÓGICAS Y ESTRÉS CORTICAL") + "\n"
	s += doubleDivider
	s += fmt.Sprintf("  ESTADO: Activo | API: 127.0.0.1:%s | EVENTOS LEÍDOS: %d\n", m.Port, len(m.HistoricalSismos))
	s += singleDivider
	s += "  ANÁLISIS DE FALLAS ACTIVAS (Boconó, San Sebastián, El Pilar):\n"
	s += singleDivider

	s += fmt.Sprintf("  %-9s  %-8s  %-10s  %-35s  %-20s  %-20s\n",
		headerStyle.Render("Fase"),
		headerStyle.Render("Celda"),
		headerStyle.Render("Sismos"),
		headerStyle.Render("Estrés Cortical (Valor-b)"),
		headerStyle.Render("Réplica Máx (Båth)"),
		headerStyle.Render("Tasa Réplicas (Omori)"))
	s += singleDivider

	projections := m.Projections

	// Apply GRAY visibility rule: if any non-GRAY cell exists in GapState,
	// hide GRAY rows. (Spec §3 — "GRAY cells MUST be hidden when any
	// non-GRAY cell exists in the snapshot".)
	hideGray := m.hasNonGrayPhase()

	faults := []string{"Falla de Boconó", "Falla de San Sebastián", "Falla de El Pilar", "Falla Desconocida"}
	grouped := make(map[string][]alert.FaultProjection)
	for _, p := range projections {
		// Filter out quiet cells (less than 3 events and no mainshock) to clean up noise
		if p.EventCount >= 3 || p.MainshockMag >= 4.0 {
			grouped[p.FaultName] = append(grouped[p.FaultName], p)
		}
	}

	totalDisplayed := 0
	for _, fName := range faults {
		list := grouped[fName]
		if len(list) == 0 {
			continue
		}

		// Filter out GRAY rows when any non-GRAY cell exists in the snapshot
		displayList := make([]alert.FaultProjection, 0, len(list))
		for _, p := range list {
			phase := m.phaseByCell(p.GridCell)
			if hideGray && phase.Phase == alert.PhaseSilencio {
				continue
			}
			displayList = append(displayList, p)
		}
		if len(displayList) == 0 {
			continue
		}

		totalDisplayed += len(displayList)
		s += "\n  " + faultNameStyle.Render("▸ "+strings.ToUpper(fName)) + "\n"

		for _, p := range displayList {
			var bValStr string
			if p.EventCount >= 5 {
				bVal := p.BValue
				if bVal < 0.70 {
					bValStr = stressHighStyle.Render(fmt.Sprintf("%.2f (Estrés Crítico ⚠️)", bVal))
				} else if bVal <= 1.20 {
					bValStr = stressNormalStyle.Render(fmt.Sprintf("%.2f (Estable / Normal)", bVal))
				} else {
					bValStr = stressLowStyle.Render(fmt.Sprintf("%.2f (Bajo Estrés)", bVal))
				}
			} else {
				bValStr = stressNDStyle.Render("N/D (Pocos sismos)")
			}

			var bathStr string
			if p.MainshockMag >= 4.0 {
				bathStr = bathMagStyle.Render(fmt.Sprintf("M %.1f max", p.BathMaxReplica))
			} else {
				bathStr = stressNDStyle.Render("N/A (Sin sismo M>=4)")
			}

			var omoriStr string
			if p.MainshockMag >= 4.0 && p.ExpectedReplicas24 > 0.01 {
				omoriStr = omoriRateStyle.Render(fmt.Sprintf("%.2f sismos/día", p.ExpectedReplicas24))
			} else {
				omoriStr = stressNDStyle.Render("N/A")
			}

			phase := m.phaseByCell(p.GridCell)
			badge := renderPhaseBadge(phase)

			s += fmt.Sprintf("  %s  %-8s  %-10d  %-35s  %-20s  %-20s\n",
				badge, cellStyle.Render(p.GridCell), p.EventCount, bValStr, bathStr, omoriStr)
		}
	}

	if totalDisplayed == 0 {
		s += "  (No se detecta actividad acumulada suficiente en ninguna falla para análisis predictivo)\n\n"
	} else {
		s += "\n"
	}

	s += singleDivider

	legendText := titleStyle.Render("¿CÓMO ENTENDER ESTA INFORMACIÓN SISMOLÓGICA?") + "\n\n" +
		"• " + headerStyle.Render("Fase de celda (GapAnalyzer):") + " Estado del enjambre en el cuadrante, clasificado en 5 niveles.\n" +
		"  - " + phaseRedStyle.Render("[RED]    ") + " Precursor: locked + >=3 sismos M>=2.0 en 12h.\n" +
		"  - " + phaseOrangeStyle.Render("[ORANGE] ") + " Réplicas: M>=4.5 dentro de 14 días (atenuado 7-14d).\n" +
		"  - " + phaseYellowStyle.Render("[YELLOW] ") + " Atención temprana: locked + 1-2 sismos M>=2.0 en 12h.\n" +
		"  - " + phaseGrayStyle.Render("[GRAY]   ") + " Silencio sísmico: locked, 0 sismos en 30d (se oculta si hay activas).\n" +
		"  - " + phaseGreenStyle.Render("[GREEN]  ") + " Estable: sin alerta.\n\n" +
		"• " + headerStyle.Render("Valor-b (Gutenberg-Richter):") + " Mide la relación entre sismos grandes y chicos. Representa el estrés acumulado.\n" +
		"  - " + stressHighStyle.Render("Estrés Crítico (<0.70):") + " Alerta de acumulación severa de energía. Peligro de sismo mayor.\n" +
		"  - " + stressNormalStyle.Render("Estable (~1.00):") + " Liberación normal y equilibrada de energía en la falla.\n" +
		"  - " + stressNDStyle.Render("N/D:") + " Se necesitan al menos 5 sismos históricos en el cuadrante para un cálculo válido.\n" +
		"  * Nota: En catálogos incompletos (donde solo detectamos magnitudes medianas/altas), el valor-b sale más bajo.\n\n" +
		"• " + headerStyle.Render("Ley de Båth:") + " Calcula la magnitud máxima esperada de una réplica en base al sismo principal del cuadrante.\n" +
		"• " + headerStyle.Render("Ley de Omori:") + " Estima la cantidad de réplicas probables a ocurrir en las próximas 24 horas."

	s += legendBoxStyle.Render(legendText) + "\n"
	s += singleDivider
	s += "  [p] Volver al panel de monitoreo | [q] Salir\n"
	s += doubleDivider
	return s
}
