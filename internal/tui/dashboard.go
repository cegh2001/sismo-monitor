package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"sismo-monitor/internal/alert"
)

var (
	magLowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#43BF6D")).Width(6).Align(lipgloss.Left)
	magMidStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8700")).Bold(true).Width(6).Align(lipgloss.Left)
	magHighStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF3B30")).Bold(true).Width(6).Align(lipgloss.Left)

	doubleDivider = strings.Repeat("=", 97) + "\n"
	singleDivider = strings.Repeat("-", 97) + "\n"

	// Predictive Monitor Premium Styles
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
)

// MsgSismo is sent when a new seismic event is processed.
type MsgSismo alert.Sismo

// MsgLog is sent to display a new log line in the TUI console.
type MsgLog string

// MsgStats is sent to update the dashboard statistic panels.
type MsgStats struct {
	TotalEvents      int
	LocalEvents      int
	EmscEvents       int
	FunvisisCount    int
	USGSEvents       int
	SimEvents        int
	InfoCount        int
	PreAlertCount    int
	CriticalCount    int
	SwarmCount       int
	SwarmQueueLen    int
	USGSPolls        int
	ActiveGaps       int
	InstabilityCount int
}

// ViewType defines the view mode of the dashboard.
type ViewType int

const (
	ViewDashboard ViewType = iota
	ViewPredictive
)

// Model represents the state of the TUI dashboard.
type Model struct {
	updateChan       <-chan tea.Msg
	Sismos           []alert.Sismo
	HistoricalSismos []alert.Sismo
	Projections      []alert.FaultProjection // Cached projections
	Logs             []string
	Stats            MsgStats
	Port             string
	statusMsg        string
	logScroll        int
	sismoScroll      int
	currentView      ViewType
}

// NewModel initializes the Bubbletea model.
func NewModel(updateChan <-chan tea.Msg, port string) Model {
	histSismos, _ := alert.LoadHistoricalSismos("data/sismos_historicos.json")
	if histSismos == nil {
		histSismos = make([]alert.Sismo, 0)
	}
	projections := alert.ComputeProjections(histSismos, time.Now())
	return Model{
		updateChan:       updateChan,
		Sismos:           make([]alert.Sismo, 0),
		HistoricalSismos: histSismos,
		Projections:      projections,
		Logs:             make([]string, 0),
		Port:             port,
		statusMsg:        "Ready",
		logScroll:        0,
		sismoScroll:      0,
		currentView:      ViewDashboard,
	}
}

// Init starts listening to the updates channel.
func (m Model) Init() tea.Cmd {
	return SubscribeToUpdates(m.updateChan)
}

// SubscribeToUpdates wraps the receive on the updates channel in a tea.Cmd.
func SubscribeToUpdates(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// Update handles incoming user key presses and background channel messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "t":
			m.statusMsg = "Triggering critical test alert..."
			return m, triggerSimulation(m.Port, 6.5, 10.60, -66.93, "La Guaira Port (Simulation)")
		case "s":
			m.statusMsg = "Triggering swarm test alerts (5 events)..."
			return m, tea.Batch(
				triggerSimulation(m.Port, 3.2, 10.58, -66.95, "Swarmland (Sim A)"),
				triggerSimulation(m.Port, 3.4, 10.59, -66.92, "Swarmland (Sim B)"),
				triggerSimulation(m.Port, 3.1, 10.61, -66.91, "Swarmland (Sim C)"),
				triggerSimulation(m.Port, 3.3, 10.60, -66.90, "Swarmland (Sim D)"),
				triggerSimulation(m.Port, 3.0, 10.57, -66.94, "Swarmland (Sim E)"),
			)
		case "i":
			m.statusMsg = "Triggering instability test alerts (5 hist, 3 swarm)..."
			return m, triggerInstabilitySimulation(m.Port)
		case "p":
			if m.currentView == ViewDashboard {
				m.currentView = ViewPredictive
				m.statusMsg = "Switched to Projections & Crustal Stress Monitor"
			} else {
				m.currentView = ViewDashboard
				m.statusMsg = "Switched to Main Dashboard"
			}
			return m, nil
		case "up":
			if len(m.Logs) > 10 {
				m.logScroll++
				if m.logScroll > len(m.Logs)-10 {
					m.logScroll = len(m.Logs) - 10
				}
				m.statusMsg = fmt.Sprintf("Scrolled up logs (%d/%d)", m.logScroll, len(m.Logs)-10)
			}
			return m, nil
		case "down":
			m.logScroll--
			if m.logScroll < 0 {
				m.logScroll = 0
			}
			if m.logScroll == 0 {
				m.statusMsg = "Ready (Live logs)"
			} else {
				m.statusMsg = fmt.Sprintf("Scrolled down logs (%d/%d)", m.logScroll, len(m.Logs)-10)
			}
			return m, nil
		case "left":
			if len(m.Sismos) > 10 {
				m.sismoScroll++
				if m.sismoScroll > len(m.Sismos)-10 {
					m.sismoScroll = len(m.Sismos) - 10
				}
				m.statusMsg = fmt.Sprintf("Scrolled up sismos (%d/%d)", m.sismoScroll, len(m.Sismos)-10)
			}
			return m, nil
		case "right":
			m.sismoScroll--
			if m.sismoScroll < 0 {
				m.sismoScroll = 0
			}
			if m.sismoScroll == 0 {
				m.statusMsg = "Ready (Live sismos)"
			} else {
				m.statusMsg = fmt.Sprintf("Scrolled down sismos (%d/%d)", m.sismoScroll, len(m.Sismos)-10)
			}
			return m, nil
		}

	case MsgSismo:
		found := false
		for i, existing := range m.Sismos {
			if existing.ID == msg.ID {
				m.Sismos[i] = alert.Sismo(msg)
				found = true
				break
			}
		}
		if !found {
			m.Sismos = append(m.Sismos, alert.Sismo(msg))
		}
		sort.Slice(m.Sismos, func(i, j int) bool {
			return m.Sismos[i].Time.Before(m.Sismos[j].Time)
		})
		if len(m.Sismos) > 50 {
			m.Sismos = m.Sismos[1:]
		}
		if m.sismoScroll > 0 {
			m.sismoScroll++
			if m.sismoScroll > len(m.Sismos)-10 {
				m.sismoScroll = len(m.Sismos) - 10
			}
		}

		// Update HistoricalSismos
		foundHist := false
		for i, existing := range m.HistoricalSismos {
			if existing.ID == msg.ID {
				m.HistoricalSismos[i] = alert.Sismo(msg)
				foundHist = true
				break
			}
		}
		if !foundHist {
			m.HistoricalSismos = append(m.HistoricalSismos, alert.Sismo(msg))
		}
		sort.Slice(m.HistoricalSismos, func(i, j int) bool {
			return m.HistoricalSismos[i].Time.Before(m.HistoricalSismos[j].Time)
		})

		// Update projections cache
		m.Projections = alert.ComputeProjections(m.HistoricalSismos, time.Now())

		return m, SubscribeToUpdates(m.updateChan)

	case MsgLog:
		// Format log with timestamp
		logLine := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), string(msg))
		m.Logs = append(m.Logs, logLine)
		if len(m.Logs) > 100 {
			m.Logs = m.Logs[1:]
		}
		if m.logScroll > 0 {
			m.logScroll++
			if m.logScroll > len(m.Logs)-10 {
				m.logScroll = len(m.Logs) - 10
			}
		}
		return m, SubscribeToUpdates(m.updateChan)

	case MsgStats:
		m.Stats = msg
		m.statusMsg = "Stats updated"
		return m, SubscribeToUpdates(m.updateChan)
	}

	return m, nil
}

func triggerSimulation(port string, mag, lat, lon float64, loc string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://localhost:%s/test-alert", port)
		payload := map[string]interface{}{
			"magnitude": mag,
			"latitude":  lat,
			"longitude": lon,
			"depth":     10.0,
			"location":  loc,
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		if err != nil {
			return MsgLog(fmt.Sprintf("TUI: Simulation trigger failed: %v", err))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return MsgLog(fmt.Sprintf("TUI: Simulation trigger status: %d", resp.StatusCode))
		}
		return MsgLog(fmt.Sprintf("TUI: Sim event dispatched: Mag %.1f Mw, Loc: %s", mag, loc))
	}
}

func triggerSimulationWithCell(port string, mag, lat, lon float64, loc string, gridCell string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://localhost:%s/test-alert", port)
		payload := map[string]interface{}{
			"magnitude": mag,
			"latitude":  lat,
			"longitude": lon,
			"depth":     10.0,
			"location":  loc,
			"grid_cell": gridCell,
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		if err != nil {
			return MsgLog(fmt.Sprintf("TUI: Simulation trigger failed: %v", err))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return MsgLog(fmt.Sprintf("TUI: Simulation trigger status: %d", resp.StatusCode))
		}
		return MsgLog(fmt.Sprintf("TUI: Sim event with cell %s dispatched: Mag %.1f Mw", gridCell, mag))
	}
}

func triggerInstabilitySimulation(port string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://localhost:%s/test-alert", port)
		payload := map[string]interface{}{
			"instability_test": true,
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		if err != nil {
			return MsgLog(fmt.Sprintf("TUI: Instability trigger failed: %v", err))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return MsgLog(fmt.Sprintf("TUI: Instability trigger status: %d", resp.StatusCode))
		}
		return MsgLog("TUI: Instability simulation batch successfully dispatched")
	}
}

// View outputs the textual representation of the dashboard.
func (m Model) View() string {
	if m.currentView == ViewPredictive {
		return m.renderPredictiveView()
	}

	var s string
	s += doubleDivider
	s += "                            VENEZUELAN SEISMIC MONITOR & ALERT SYSTEM\n"
	s += doubleDivider
	s += fmt.Sprintf("  STATUS: Active | EMSC: Connected | Funvisis: Polling | USGS: Polling | API: 127.0.0.1:%s\n", m.Port)
	s += fmt.Sprintf("  ACTION: %s\n", m.statusMsg)
	s += singleDivider
	s += "  STATISTICS:\n"
	s += fmt.Sprintf("  Total Events: %-3d | Local (<300km): %-3d | EMSC: %-3d | Funvisis: %-3d | USGS: %-3d | Sim: %-3d\n",
		m.Stats.TotalEvents, m.Stats.LocalEvents, m.Stats.EmscEvents, m.Stats.FunvisisCount, m.Stats.USGSEvents, m.Stats.SimEvents)
	s += fmt.Sprintf("  Threat Levels: Info: %-3d | Pre-Alert: %-3d | Critical: %-3d | Swarm: %-3d | Instability: %-3d\n",
		m.Stats.InfoCount, m.Stats.PreAlertCount, m.Stats.CriticalCount, m.Stats.SwarmCount, m.Stats.InstabilityCount)
	s += fmt.Sprintf("  USGS Polls: %-3d | Active Gaps (Lock Segments): %-3d | Swarm Queue: %-3d\n",
		m.Stats.USGSPolls, m.Stats.ActiveGaps, m.Stats.SwarmQueueLen)
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
	s += "  [q] Quit | [t] Trigger Critical | [s] Trigger Swarm | [i] Trigger Instability | [p] Projections\n"
	s += "  [Arrows] Up/Down: Scroll Logs | Left/Right: Scroll Sismos\n"
	s += doubleDivider
	return s
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

	s += fmt.Sprintf("  %-8s  %-10s  %-35s  %-20s  %-20s\n",
		headerStyle.Render("Celda"),
		headerStyle.Render("Sismos"),
		headerStyle.Render("Estrés Cortical (Valor-b)"),
		headerStyle.Render("Réplica Máx (Båth)"),
		headerStyle.Render("Tasa Réplicas (Omori)"))
	s += singleDivider

	projections := m.Projections

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

		totalDisplayed += len(list)
		s += "\n  " + faultNameStyle.Render("▸ "+strings.ToUpper(fName)) + "\n"

		for _, p := range list {
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

			s += fmt.Sprintf("  %-8s  %-10d  %-35s  %-20s  %-20s\n",
				cellStyle.Render(p.GridCell), p.EventCount, bValStr, bathStr, omoriStr)
		}
	}

	if totalDisplayed == 0 {
		s += "  (No se detecta actividad acumulada suficiente en ninguna falla para análisis predictivo)\n\n"
	} else {
		s += "\n"
	}

	s += singleDivider

	legendText := titleStyle.Render("¿CÓMO ENTENDER ESTA INFORMACIÓN SISMOLÓGICA?") + "\n\n" +
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

func truncate(str string, length int) string {
	runes := []rune(str)
	if len(runes) > length {
		return string(runes[:length-3]) + "..."
	}
	return str
}
