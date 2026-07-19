package tui

import (
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

	gemmaSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
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

// MsgGemmaSpinnerTick triggers next frame of spinner animation.
type MsgGemmaSpinnerTick struct{}

// MsgGapState is sent by the coordinator each event cycle with the
// snapshot of per-cell phase classifications (RED/ORANGE/YELLOW/GRAY/GREEN).
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
	updateChan          <-chan tea.Msg
	Sismos              []alert.Sismo
	HistoricalSismos    []alert.Sismo
	Projections         []alert.FaultProjection // Cached projections
	GapState            []alert.CellPhase       // Per-cell phase snapshot (RED/ORANGE/YELLOW/GRAY/GREEN)
	GemmaReports        []alert.SynthesisResponse // LLM narrative reports from Gemma 4
	Logs                []string
	Stats               MsgStats
	Port                string
	statusMsg           string
	logScroll           int
	sismoScroll         int
	predictiveScroll    int
	gemmaScroll         int
	gemmaSelectedReport int
	gemmaBodyScroll     int
	gemmaSpinnerFrame   int
	termHeight          int
	termWidth           int
	currentView         ViewType
	startTime           time.Time
	gemmaGenerating     bool
	gemmaError          string
}
