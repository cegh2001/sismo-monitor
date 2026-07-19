package tui

import (
	"fmt"
	"strings"
)

// View outputs the textual representation of the dashboard.
func (m Model) View() string {
	if m.currentView == ViewPredictive {
		full := m.renderPredictiveView()
		lines := strings.Split(full, "\n")

		// Clamp scroll: 0 = top of content
		maxScroll := len(lines) - m.termHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.predictiveScroll > maxScroll {
			m.predictiveScroll = maxScroll
		}
		if m.predictiveScroll < 0 {
			m.predictiveScroll = 0
		}

		end := m.predictiveScroll + m.termHeight
		if end > len(lines) {
			end = len(lines)
		}

		visible := lines[m.predictiveScroll:end]

		// Scroll indicators
		if m.predictiveScroll > 0 {
			visible[0] = "▲ Scroll up for more  " + visible[0]
		}
		if end < len(lines) {
			visible[len(visible)-1] = visible[len(visible)-1] + "  ▼ Scroll down for more"
		}

		return strings.Join(visible, "\n")
	}

	if m.currentView == ViewGemma {
		return m.renderGemmaView()
	}

	var s string
	s += doubleDivider
	s += "                            VENEZUELAN SEISMIC MONITOR & ALERT SYSTEM\n"
	s += doubleDivider
	s += fmt.Sprintf("  STATUS: Active | EMSC: Connected | Funvisis: Polling | USGS: Polling | API: 127.0.0.1:%s\n", m.Port)
	s += fmt.Sprintf("  ACTION: %s\n", m.statusMsg)
	s += singleDivider
	s += "  STATISTICS:\n"
	s += fmt.Sprintf("  Total: %-3d | Local: %-3d | Swarms: %-2d | Instab: %-2d | Active Gaps: %-2d | SwarmQ: %-2d\n",
		m.Stats.TotalEvents, m.Stats.LocalEvents, m.Stats.SwarmCount, m.Stats.InstabilityCount, m.Stats.ActiveGaps, m.Stats.SwarmQueueLen)
	s += fmt.Sprintf("  Sources: EMSC:%-3d | Funvisis:%-3d | USGS:%-3d | USGS_FDSN:%-3d | SGC:%-3d | Sim:%-3d\n",
		m.Stats.EmscEvents, m.Stats.FunvisisCount, m.Stats.USGSEvents, m.Stats.SgcEvents, m.Stats.IrisEvents, m.Stats.SimEvents)
	s += fmt.Sprintf("  Threat Levels: Info:%-3d | Pre-Alert:%-3d | Critical:%-3d | USGS Polls:%-3d\n",
		m.Stats.InfoCount, m.Stats.PreAlertCount, m.Stats.CriticalCount, m.Stats.USGSPolls)
	s += singleDivider
	s += "  LATEST SEISMIC EVENTS (Use Left/Right Arrows to scroll):\n"
	s += fmt.Sprintf("  %-10s  %-8s  %-6s  %-8s  %-8s  %-45s\n", "Source", "Time", "Mag", "Depth", "Distance", "Location")
	if len(m.Sismos) == 0 {
		s += "  (No seismic events processed yet)\n"
	} else {
		totalSismos := len(m.Sismos)
		start := totalSismos - 1 - m.sismoScroll
		end := start - 10
		if end < -1 {
			end = -1
		}
		for i := start; i > end; i-- {
			ev := m.Sismos[i]
			tStr := ev.Time.Local().Format("15:04:05")

			magStr := fmt.Sprintf("%.1f", ev.Magnitude)
			var styledMag string
			if ev.Magnitude < 3.5 {
				styledMag = magLowStyle.Render(magStr)
			} else if ev.Magnitude < 5.0 {
				styledMag = magMidStyle.Render(magStr)
			} else {
				styledMag = magHighStyle.Render(magStr)
			}

			s += fmt.Sprintf("  %-10s  %-8s  %s  %-8.1f  %-8.1f  %-45s\n",
				ev.Source, tStr, styledMag, ev.Depth, ev.Distance, truncate(ev.Location, 45))
		}
		if m.sismoScroll > 0 {
			s = strings.Replace(s, "  LATEST SEISMIC EVENTS", "  LATEST SEISMIC EVENTS [▲ More recent sismos above]", 1)
		}
		if start-10 >= 0 {
			s = strings.Replace(s, "scroll):", "scroll) [▼ Older sismos below]:", 1)
		}
	}
	s += singleDivider
	s += "  LATEST SYSTEM LOGS (Use Up/Down Arrows to scroll):\n"
	if len(m.Logs) == 0 {
		s += "  (No logs recorded yet)\n"
	} else {
		totalLogs := len(m.Logs)
		start := totalLogs - 10 - m.logScroll
		if start < 0 {
			start = 0
		}
		end := start + 10
		if end > totalLogs {
			end = totalLogs
		}
		for i := start; i < end; i++ {
			s += fmt.Sprintf("  * %s\n", m.Logs[i])
		}
		if start > 0 {
			s = strings.Replace(s, "  LATEST SYSTEM LOGS", "  LATEST SYSTEM LOGS [▲ More logs above]", 1)
		}
		if end < totalLogs {
			s = strings.Replace(s, "scroll):", "scroll) [▼ More logs below]:", 1)
		}
	}
	s += singleDivider
	s += "  [q] Quit | [g] Análisis Gemma 4 | [t] Trigger Critical | [s] Trigger Swarm | [i] Trigger Instability | [p] Projections\n"
	s += "  [Arrows] Up/Down: Scroll Logs | Left/Right: Scroll Sismos\n"
	s += doubleDivider
	return s
}

func truncate(str string, length int) string {
	runes := []rune(str)
	if len(runes) > length {
		return string(runes[:length-3]) + "..."
	}
	return str
}
