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
	"sismo-monitor/internal/alert"
)

func tickGemmaSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return MsgGemmaSpinnerTick{}
	})
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
		predictiveScroll: 0,
		termHeight:       24,
		termWidth:        97,
		currentView:      ViewDashboard,
		startTime:        time.Now(),
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
	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		m.termWidth = msg.Width
		return m, nil

	case tea.KeyMsg:
		if m.currentView == ViewGemma {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "d":
				m.currentView = ViewDashboard
				m.statusMsg = "Switched to Main Dashboard"
				return m, nil
			case "p":
				m.currentView = ViewPredictive
				m.predictiveScroll = 0
				m.statusMsg = "Switched to Projections & Crustal Stress Monitor"
				return m, nil
			case "g":
				if time.Since(m.startTime) < simCooldown {
					m.gemmaError = "Cooldown activo — espere 2s después del inicio"
					return m, nil
				}
				if m.gemmaGenerating {
					m.statusMsg = "⚠️ Generación en curso. Por favor espere..."
					return m, nil
				}
				m.gemmaError = ""
				m.gemmaGenerating = true
				m.gemmaSpinnerFrame = 0
				m.statusMsg = "Solicitando análisis sismológico a Gemma 4 (Google Search Grounded)..."
				return m, tea.Batch(triggerGemmaAnalysis(m.Port), tickGemmaSpinner())
			case "left", "h", "[":
				if len(m.GemmaReports) > 0 {
					m.gemmaSelectedReport--
					if m.gemmaSelectedReport < 0 {
						m.gemmaSelectedReport = 0
					}
					m.gemmaBodyScroll = 0
				}
				return m, nil
			case "right", "l", "]":
				if len(m.GemmaReports) > 0 {
					m.gemmaSelectedReport++
					if m.gemmaSelectedReport >= len(m.GemmaReports) {
						m.gemmaSelectedReport = len(m.GemmaReports) - 1
					}
					m.gemmaBodyScroll = 0
				}
				return m, nil
			case "tab":
				if len(m.GemmaReports) > 0 {
					m.gemmaSelectedReport = (m.gemmaSelectedReport + 1) % len(m.GemmaReports)
					m.gemmaBodyScroll = 0
				}
				return m, nil
			case "up", "k":
				if m.gemmaBodyScroll > 0 {
					m.gemmaBodyScroll--
				}
				return m, nil
			case "down", "j":
				m.gemmaBodyScroll++
				return m, nil
			case "pgup", "b":
				m.gemmaBodyScroll -= 8
				if m.gemmaBodyScroll < 0 {
					m.gemmaBodyScroll = 0
				}
				return m, nil
			case "pgdown", "f", "space":
				m.gemmaBodyScroll += 8
				return m, nil
			case "home":
				m.gemmaBodyScroll = 0
				return m, nil
			}
			return m, nil
		}

		if m.currentView == ViewPredictive {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "p":
				m.currentView = ViewDashboard
				m.predictiveScroll = 0
				m.statusMsg = "Switched to Main Dashboard"
				return m, nil
			case "up":
				m.predictiveScroll--
				if m.predictiveScroll < 0 {
					m.predictiveScroll = 0
				}
				return m, nil
			case "down":
				m.predictiveScroll++
				return m, nil
			case "pgup":
				m.predictiveScroll -= 10
				if m.predictiveScroll < 0 {
					m.predictiveScroll = 0
				}
				return m, nil
			case "pgdown":
				m.predictiveScroll += 10
				return m, nil
			case "home":
				m.predictiveScroll = 0
				return m, nil
			case "g":
				if time.Since(m.startTime) < simCooldown {
					m.statusMsg = "Startup cooldown active — manual Gemma analysis disabled for 2s"
					return m, nil
				}
				m.currentView = ViewGemma
				m.gemmaScroll = 0
				m.gemmaError = ""
				m.gemmaGenerating = true
				m.statusMsg = "Solicitando análisis sismológico a Gemma 4 (Google Search Grounded)..."
				return m, triggerGemmaAnalysis(m.Port)
			case "end":
				content := m.renderPredictiveView()
				totalLines := strings.Count(content, "\n") + 1
				maxScroll := totalLines - m.termHeight
				if maxScroll < 0 {
					maxScroll = 0
				}
				m.predictiveScroll = maxScroll
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "t":
			if time.Since(m.startTime) < simCooldown {
				m.statusMsg = "Startup cooldown active — simulation triggers disabled for 2s"
				return m, nil
			}
			m.statusMsg = "Triggering critical test alert..."
			return m, triggerSimulation(m.Port, 6.5, 10.60, -66.93, "La Guaira Port (Simulation)")
		case "s":
			if time.Since(m.startTime) < simCooldown {
				m.statusMsg = "Startup cooldown active — simulation triggers disabled for 2s"
				return m, nil
			}
			m.statusMsg = "Triggering swarm test alerts (5 events)..."
			return m, tea.Batch(
				triggerSimulation(m.Port, 3.2, 10.58, -66.95, "Swarmland (Sim A)"),
				triggerSimulation(m.Port, 3.4, 10.59, -66.92, "Swarmland (Sim B)"),
				triggerSimulation(m.Port, 3.1, 10.61, -66.91, "Swarmland (Sim C)"),
				triggerSimulation(m.Port, 3.3, 10.60, -66.90, "Swarmland (Sim D)"),
				triggerSimulation(m.Port, 3.0, 10.57, -66.94, "Swarmland (Sim E)"),
			)
		case "i":
			if time.Since(m.startTime) < simCooldown {
				m.statusMsg = "Startup cooldown active — simulation triggers disabled for 2s"
				return m, nil
			}
			m.statusMsg = "Triggering instability test alerts (5 hist, 3 swarm)..."
			return m, triggerInstabilitySimulation(m.Port)
		case "g":
			if time.Since(m.startTime) < simCooldown {
				m.statusMsg = "Startup cooldown active — manual Gemma analysis disabled for 2s"
				return m, nil
			}
			m.currentView = ViewGemma
			m.gemmaScroll = 0
			m.gemmaError = ""
			m.gemmaGenerating = true
			m.statusMsg = "Solicitando análisis sismológico a Gemma 4 (Google Search Grounded)..."
			return m, triggerGemmaAnalysis(m.Port)
		case "p":
			m.currentView = ViewPredictive
			m.predictiveScroll = 0
			m.statusMsg = "Switched to Projections & Crustal Stress Monitor"
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

	case MsgGemmaSpinnerTick:
		if m.gemmaGenerating {
			m.gemmaSpinnerFrame = (m.gemmaSpinnerFrame + 1) % len(gemmaSpinnerFrames)
			return m, tickGemmaSpinner()
		}
		return m, nil

	case MsgGemmaReport:
		m.GemmaReports = append(m.GemmaReports, msg.Report)
		if len(m.GemmaReports) > 20 {
			m.GemmaReports = m.GemmaReports[len(m.GemmaReports)-20:]
		}
		m.gemmaSelectedReport = len(m.GemmaReports) - 1
		m.gemmaBodyScroll = 0
		m.gemmaGenerating = false
		m.gemmaError = ""
		m.statusMsg = fmt.Sprintf("✅ Reporte Gemma 4 ([%s]) recibido", msg.Report.ReportType)
		return m, SubscribeToUpdates(m.updateChan)

	case MsgGemmaStatus:
		m.gemmaGenerating = msg.Generating
		if msg.Message != "" {
			m.statusMsg = msg.Message
			if strings.Contains(msg.Message, "❌") || strings.Contains(msg.Message, "Error") {
				m.gemmaError = msg.Message
				m.gemmaGenerating = false
			}
		}
		if m.gemmaGenerating {
			return m, tea.Batch(SubscribeToUpdates(m.updateChan), tickGemmaSpinner())
		}
		return m, SubscribeToUpdates(m.updateChan)

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

	case MsgGapState:
		phases := msg.Phases
		if phases == nil {
			phases = []alert.CellPhase{}
		}
		m.GapState = phases
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

func triggerGemmaAnalysis(port string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://localhost:%s/api/gemma/analyze", port)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte("{}")))
		if err != nil {
			return MsgGemmaStatus{
				Generating: false,
				Message:    fmt.Sprintf("❌ Error al conectar con API Gemma: %v", err),
			}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return MsgGemmaStatus{
				Generating: false,
				Message:    fmt.Sprintf("❌ Solicitud a Gemma rechazada (HTTP %d)", resp.StatusCode),
			}
		}
		return MsgGemmaStatus{
			Generating: true,
			Message:    "🤖 Generando análisis sismológico con Gemma 4 (Google Search Grounding)... Por favor espere.",
		}
	}
}
