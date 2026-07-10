package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	updateChan  <-chan tea.Msg
	Sismos      []alert.Sismo
	Logs        []string
	Stats       MsgStats
	Port        string
	statusMsg   string
	logScroll   int
	sismoScroll int
}

// NewModel initializes the Bubbletea model.
func NewModel(updateChan <-chan tea.Msg, port string) Model {
	return Model{
		updateChan:  updateChan,
		Sismos:      make([]alert.Sismo, 0),
		Logs:        make([]string, 0),
		Port:        port,
		statusMsg:   "Ready",
		logScroll:   0,
		sismoScroll: 0,
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
		m.Sismos = append(m.Sismos, alert.Sismo(msg))
		if len(m.Sismos) > 50 {
			m.Sismos = m.Sismos[1:]
		}
		if m.sismoScroll > 0 {
			m.sismoScroll++
			if m.sismoScroll > len(m.Sismos)-10 {
				m.sismoScroll = len(m.Sismos) - 10
			}
		}
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
	s += "  LATEST SEISMIC EVENTS (Use Left/Right Arrows to scroll):\n"
	s += fmt.Sprintf("  %-10s  %-8s  %-6s  %-8s  %-8s  %-30s\n", "Source", "Time", "Mag", "Depth", "Distance", "Location")
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
			s += fmt.Sprintf("  %-10s  %-8s  %-6.1f  %-8.1f  %-8.1f  %-30s\n",
				ev.Source, tStr, ev.Magnitude, ev.Depth, ev.Distance, truncate(ev.Location, 30))
		}
		if m.sismoScroll > 0 {
			s = strings.Replace(s, "  LATEST SEISMIC EVENTS", "  LATEST SEISMIC EVENTS [▲ More recent sismos above]", 1)
		}
		if start-10 >= 0 {
			s = strings.Replace(s, "scroll):", "scroll) [▼ Older sismos below]:", 1)
		}
	}
	s += "--------------------------------------------------------------------------------\n"
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
	s += "--------------------------------------------------------------------------------\n"
	s += "  [q] Quit | [t] Trigger Critical Alert (6.5 Mw) | [s] Trigger Swarm Alert (>=5 events)\n"
	s += "  [Arrows] Up/Down: Scroll Logs | Left/Right: Scroll Sismos\n"
	s += "================================================================================\n"
	return s
}

func truncate(str string, length int) string {
	if len(str) > length {
		return str[:length-3] + "..."
	}
	return str
}
