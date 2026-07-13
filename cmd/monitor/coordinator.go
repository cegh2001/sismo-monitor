package main

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/tui"
)

// gapPhaseCooldown is the minimum interval between Pushover notifications
// for the same grid cell. Prevents flooding during sustained swarm activity.
const gapPhaseCooldown = 30 * time.Minute

// Coordinator drives the event cycle: gap analysis -> phase classification
// -> TUI snapshot -> optional transition notification. It is the bridge
// between GapAnalyzer (data) and the TUI (visual).
type Coordinator struct {
	gapAnalyzer *alert.GapAnalyzer
	notifier    alert.Notifier
	tuiChan     chan<- tea.Msg
	log         func(string, ...interface{})

	// Per-cell previous phase (used for transition detection).
	prevState map[string]alert.SwarmPhase
	// Per-cell last-notified time (cooldown).
	cooldown map[string]time.Time
}

// NewCoordinator wires a Coordinator with its dependencies. log may be nil.
func NewCoordinator(gap *alert.GapAnalyzer, notif alert.Notifier, tuiChan chan<- tea.Msg, log func(string, ...interface{})) *Coordinator {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	return &Coordinator{
		gapAnalyzer: gap,
		notifier:    notif,
		tuiChan:     tuiChan,
		log:         log,
		prevState:   make(map[string]alert.SwarmPhase),
		cooldown:    make(map[string]time.Time),
	}
}

// BuildGapSnapshot computes the per-cell phase snapshot for the current
// cycle. Iterates ALL known cells (locked + recently active) so that ORANGE
// state (M>=4.5 within 14d) is captured even on cells that have un-locked
// due to that same mainshock. Returns an empty (non-nil) slice when the
// analyzer is empty. Exposed for testing.
func (c *Coordinator) BuildGapSnapshot(now time.Time) []alert.CellPhase {
	cells := c.gapAnalyzer.GetAllCells()
	if len(cells) == 0 {
		return []alert.CellPhase{}
	}
	since := now.Add(-30 * 24 * time.Hour)
	phases := make([]alert.CellPhase, 0, len(cells))
	for _, cell := range cells {
		events := c.gapAnalyzer.GetCellEvents(cell, since)
		locked := c.gapAnalyzer.IsLocked(cell, now)
		phase := alert.ClassifyCellPhase(locked, events, now)
		phase.GridCell = cell
		phases = append(phases, phase)
	}
	return phases
}

// shouldNotifyOnTransition returns true if the (previous -> current) transition
// warrants a Pushover alert AND the per-cell cooldown has elapsed.
//
// Triggered transitions (transition only, not steady-state):
//   - any non-ORANGE -> PhaseReplicas (ORANGE)
//   - PhaseEstable -> PhaseAtencion (GREEN -> YELLOW)
//   - PhaseEstable -> PhasePrecursor (GREEN -> RED)
//   - PhaseAtencion -> PhasePrecursor (YELLOW -> RED)
//   - PhaseSilencio -> PhaseAtencion (GRAY -> YELLOW, reactivation)
//   - PhaseSilencio -> PhasePrecursor (GRAY -> RED, reactivation)
//
// Steady-state (prev == curr) and decay-fade (e.g., ORANGE -> GREEN) are
// silent. The 30-minute cooldown is the source of truth for de-duplication
// of ORANGE re-entries.
func (c *Coordinator) shouldNotifyOnTransition(cell string, prev, curr alert.SwarmPhase, now time.Time) bool {
	notify := false
	switch {
	case curr == alert.PhaseReplicas && prev != alert.PhaseReplicas:
		notify = true
	case prev == alert.PhaseEstable && curr == alert.PhaseAtencion:
		notify = true
	case prev == alert.PhaseEstable && curr == alert.PhasePrecursor:
		notify = true
	case prev == alert.PhaseAtencion && curr == alert.PhasePrecursor:
		notify = true
	case prev == alert.PhaseSilencio && curr == alert.PhaseAtencion:
		notify = true
	case prev == alert.PhaseSilencio && curr == alert.PhasePrecursor:
		notify = true
	}
	if !notify {
		return false
	}
	// Cooldown check: skip if the last notification for this cell is within
	// the cooldown window.
	if last, ok := c.cooldown[cell]; ok {
		if now.Sub(last) < gapPhaseCooldown {
			return false
		}
	}
	return true
}

// EmitGapSnapshot pushes the current snapshot to the TUI and triggers any
// transition-driven notifications. Safe to call from the coordinator loop.
// Returns the snapshot that was emitted (handy for tests).
func (c *Coordinator) EmitGapSnapshot(ctx context.Context, now time.Time) []alert.CellPhase {
	snapshot := c.BuildGapSnapshot(now)
	c.sendTui(tui.MsgGapState{Phases: snapshot})

	// Detect transitions per cell and fire notifier when appropriate.
	for _, ph := range snapshot {
		prev := c.prevState[ph.GridCell]
		if c.shouldNotifyOnTransition(ph.GridCell, prev, ph.Phase, now) {
			// Use the most recent event in the cell as the alert payload.
			// We pick the largest-magnitude event in the last 30 days if any.
			events := c.gapAnalyzer.GetCellEvents(ph.GridCell, now.Add(-30*24*time.Hour))
			payload := lastMostRelevantEvent(events)
			if payload.ID == "" {
				// Fall back to a synthetic event so the notifier has something
				// to attach to.
				payload = alert.Sismo{
					ID:        "phase-transition",
					Source:    "GapAnalyzer",
					GridCell:  ph.GridCell,
					Magnitude: ph.MainshockMag,
					Time:      ph.MainshockTime,
				}
			}
			c.log("Phase transition %v -> %v for cell %s, dispatching alert", prev, ph.Phase, ph.GridCell)
			_ = c.notifier.Notify(ctx, alert.Alert{
				Sismo: payload,
				Level: alert.LevelInstability,
			})
			c.cooldown[ph.GridCell] = now
		}
		c.prevState[ph.GridCell] = ph.Phase
	}
	return snapshot
}

func (c *Coordinator) sendTui(msg tea.Msg) {
	select {
	case c.tuiChan <- msg:
	default:
	}
}

// lastMostRelevantEvent picks the event most useful to surface in a
// notification: the largest-magnitude one in the slice. Returns zero-value
// Sismo if the slice is empty.
func lastMostRelevantEvent(events []alert.Sismo) alert.Sismo {
	var best alert.Sismo
	for _, e := range events {
		if e.Magnitude > best.Magnitude {
			best = e
		}
	}
	return best
}
