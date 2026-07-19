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
)

// MsgSismo is sent when a new seismic event is processed.
type MsgSismo alert.Sismo

// MsgLog is sent to display a new log line in the TUI console.
type MsgLog string

// MsgGemmaReport is sent when a new LLM narrative report from Gemma 4 is available.
type MsgGemmaReport struct {
	Report alert.SynthesisResponse
}

// MsgGemmaStatus is sent to update the TUI status on Gemma 4 generation state (generating, success, error).
type MsgGemmaStatus struct {
	Generating bool
	Message    string
}

// MsgGapState is sent by the coordinator each event cycle with the
// snapshot of per-cell phase classifications (RED/ORANGE/YELLOW/GRAY/GREEN).
// Stored on Model.GapState and merged into the predictive view at render time.
type MsgGapState struct {
	Phases []alert.CellPhase
}

// MsgStats is sent to update the dashboard statistic panels.
type MsgStats struct {
	TotalEvents      int
	LocalEvents      int
	EmscEvents       int
	FunvisisCount    int
	USGSEvents       int
	SgcEvents        int
	IrisEvents       int
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
	ViewGemma

	// simCooldown prevents spurious terminal input (common on Windows) from
	// accidentally triggering simulations during program startup.
	simCooldown = 2 * time.Second
)

// Model represents the state of the TUI dashboard.
type Model struct {
	updateChan       <-chan tea.Msg
	Sismos           []alert.Sismo
	HistoricalSismos []alert.Sismo
	Projections      []alert.FaultProjection // Cached projections
	GapState         []alert.CellPhase       // Per-cell phase snapshot (RED/ORANGE/YELLOW/GRAY/GREEN)
	GemmaReports     []alert.SynthesisResponse // LLM narrative reports from Gemma 4
	Logs             []string
	Stats            MsgStats
	Port             string
	statusMsg        string
	logScroll        int
	sismoScroll      int
	predictiveScroll int
	gemmaScroll      int
	termHeight       int
	termWidth        int
	currentView      ViewType
	startTime        time.Time
	gemmaGenerating  bool
	gemmaError       string
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
				m.gemmaError = ""
				m.gemmaGenerating = true
				m.statusMsg = "Solicitando análisis sismológico a Gemma 4 (Google Search Grounded)..."
				return m, triggerGemmaAnalysis(m.Port)
			case "up":
				maxScroll := len(m.GemmaReports) - 1
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.gemmaScroll < maxScroll {
					m.gemmaScroll++
				}
				return m, nil
			case "down":
				if m.gemmaScroll > 0 {
					m.gemmaScroll--
				}
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
				// Calculate bottom position from actual content
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

	case MsgGemmaReport:
		m.GemmaReports = append(m.GemmaReports, msg.Report)
		if len(m.GemmaReports) > 20 {
			m.GemmaReports = m.GemmaReports[len(m.GemmaReports)-20:]
		}
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
			}
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
