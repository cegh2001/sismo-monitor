package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"sismo-monitor/internal/alert"
)

// MsgSismo is sent when a new seismic event is processed.
type MsgSismo alert.Sismo

// MsgLog is sent to display a new log line in the TUI console.
type MsgLog string

// MsgStats is sent to update the dashboard statistic panels.
type MsgStats struct {
	TotalEvents   int
	LocalEvents   int
	EmscEvents    int
	FunvisisCount int
	SimEvents     int
	InfoCount     int
	PreAlertCount int
	CriticalCount int
	SwarmCount    int
	SwarmQueueLen int
}

// Model represents the state of the TUI dashboard.
type Model struct {
	updateChan <-chan tea.Msg
	Sismos     []alert.Sismo
	Logs       []string
	Stats      MsgStats
	Port       string
	statusMsg  string
}

// NewModel initializes the Bubbletea model.
func NewModel(updateChan <-chan tea.Msg, port string) Model {
	return Model{
		updateChan: updateChan,
		Sismos:     make([]alert.Sismo, 0),
		Logs:       make([]string, 0),
		Port:       port,
		statusMsg:  "Ready",
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
			m.statusMsg = "Triggering swarm test alerts..."
			return m, tea.Batch(
				triggerSimulation(m.Port, 3.2, 10.58, -66.95, "Swarmland (Sim A)"),
				triggerSimulation(m.Port, 3.4, 10.59, -66.92, "Swarmland (Sim B)"),
				triggerSimulation(m.Port, 3.1, 10.61, -66.91, "Swarmland (Sim C)"),
			)
		}

	case MsgSismo:
		m.Sismos = append(m.Sismos, alert.Sismo(msg))
		if len(m.Sismos) > 10 {
			m.Sismos = m.Sismos[1:]
		}
		return m, SubscribeToUpdates(m.updateChan)

	case MsgLog:
		// Format log with timestamp
		logLine := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), string(msg))
		m.Logs = append(m.Logs, logLine)
		if len(m.Logs) > 10 {
			m.Logs = m.Logs[1:]
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

// View outputs the textual representation of the dashboard.
func (m Model) View() string {
	var s string
	s += "================================================================================\n"
	s += "                    VENEZUELAN SEISMIC MONITOR & ALERT SYSTEM\n"
	s += "================================================================================\n"
	s += fmt.Sprintf("  STATUS: Active | EMSC WS: Connected | Funvisis Scraper: Polling | API: :%s\n", m.Port)
	s += fmt.Sprintf("  ACTION: %s\n", m.statusMsg)
	s += "--------------------------------------------------------------------------------\n"
	s += "  STATISTICS:\n"
	s += fmt.Sprintf("  Total Events: %-3d | Local (<300km): %-3d | EMSC: %-3d | Funvisis: %-3d | Sim: %-3d\n",
		m.Stats.TotalEvents, m.Stats.LocalEvents, m.Stats.EmscEvents, m.Stats.FunvisisCount, m.Stats.SimEvents)
	s += fmt.Sprintf("  Threat Levels: Info: %-3d | Pre-Alert: %-3d | Critical: %-3d | Swarm: %-3d\n",
		m.Stats.InfoCount, m.Stats.PreAlertCount, m.Stats.CriticalCount, m.Stats.SwarmCount)
	s += fmt.Sprintf("  Swarms Detected: %-3d (Active local events in 6h window: %d)\n", m.Stats.SwarmCount, m.Stats.SwarmQueueLen)
	s += "--------------------------------------------------------------------------------\n"
	s += "  LATEST SEISMIC EVENTS:\n"
	s += fmt.Sprintf("  %-10s  %-8s  %-6s  %-8s  %-8s  %-30s\n", "Source", "Time", "Mag", "Depth", "Distance", "Location")
	if len(m.Sismos) == 0 {
		s += "  (No seismic events processed yet)\n"
	} else {
		for i := len(m.Sismos) - 1; i >= 0; i-- {
			ev := m.Sismos[i]
			tStr := ev.Time.Format("15:04:05")
			s += fmt.Sprintf("  %-10s  %-8s  %-6.1f  %-8.1f  %-8.1f  %-30s\n",
				ev.Source, tStr, ev.Magnitude, ev.Depth, ev.Distance, truncate(ev.Location, 30))
		}
	}
	s += "--------------------------------------------------------------------------------\n"
	s += "  LATEST SYSTEM LOGS:\n"
	if len(m.Logs) == 0 {
		s += "  (No logs recorded yet)\n"
	} else {
		for i := len(m.Logs) - 1; i >= 0; i-- {
			s += fmt.Sprintf("  * %s\n", m.Logs[i])
		}
	}
	s += "--------------------------------------------------------------------------------\n"
	s += "  [q] Quit | [t] Trigger Critical Alert (6.5 Mw) | [s] Trigger Swarm Alert (>=3 events)\n"
	s += "================================================================================\n"
	return s
}

func truncate(str string, length int) string {
	if len(str) > length {
		return str[:length-3] + "..."
	}
	return str
}
