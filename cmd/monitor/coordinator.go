package main

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/llm"
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
	gemma       *llm.GemmaSynthesizer
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

// SetGemmaSynthesizer configures the LLM narrative synthesizer component.
func (c *Coordinator) SetGemmaSynthesizer(g *llm.GemmaSynthesizer) {
	c.gemma = g
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

			if c.gemma != nil {
				go func(cell string, prevPhase, currPhase alert.SwarmPhase, mainshock alert.Sismo, evts []alert.Sismo) {
					proj := alert.AnalyzeGridCell(cell, evts, now)
					req := llm.SynthesisRequest{
						TriggerType:    fmt.Sprintf("TRANSICION_FASE_%v_A_%v", prevPhase, currPhase),
						FaultName:      alert.GetFaultName(mainshock.Latitude, mainshock.Longitude),
						CellID:         cell,
						BValue:         proj.BValue,
						WeightedEnergy: proj.WeightedEnergy,
						DynamicRate:    proj.DynamicRate,
						Mainshock:      mainshock,
						RecentEvents:   evts,
						Phase:          currPhase,
					}
					resp, err := c.gemma.Synthesize(ctx, req)
					if err != nil {
						c.log("Gemma 4 synthesis failed for cell %s: %v", cell, err)
						return
					}
					c.sendTui(tui.MsgGemmaReport{Report: resp})
					if pn, ok := c.notifier.(*alert.PushoverNotifier); ok {
						_ = pn.SendSynthesisReport(resp)
					}
				}(ph.GridCell, prev, ph.Phase, payload, events)
			}
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

// TriggerManualAnalysis forces an immediate Gemma 4 natural language analysis
// of the active seismic state and pushes the narrative report to TUI and Pushover.
func (c *Coordinator) TriggerManualAnalysis(ctx context.Context) {
	if c.gemma == nil {
		c.log("Manual Gemma 4 analysis requested, but GemmaSynthesizer is disabled (GEMINI_API_KEY missing)")
		c.sendTui(tui.MsgGemmaStatus{
			Generating: false,
			Message:    "❌ No se puede generar análisis: GEMINI_API_KEY no configurada en .env",
		})
		return
	}

	now := time.Now()
	snapshot := c.BuildGapSnapshot(now)

	var targetCell alert.CellPhase
	found := false
	for _, ph := range snapshot {
		if ph.Phase != alert.PhaseEstable && ph.Phase != alert.PhaseSilencio {
			targetCell = ph
			found = true
			break
		}
	}
	if !found && len(snapshot) > 0 {
		targetCell = snapshot[0]
	}

	cellID := targetCell.GridCell
	if cellID == "" {
		cellID = "G_22_12" // Fallback to El Pilar active cell
	}

	events := c.gapAnalyzer.GetCellEvents(cellID, now.Add(-30*24*time.Hour))
	mainshock := lastMostRelevantEvent(events)
	if mainshock.ID == "" {
		mainshock = alert.Sismo{
			ID:        "manual-analysis",
			Source:    "TUI-Manual",
			GridCell:  cellID,
			Magnitude: 3.6,
			Depth:     10.0,
			Latitude:  10.5,
			Longitude: -64.2,
			Location:  "Falla de El Pilar (Análisis Manual)",
			Time:      now,
		}
	}

	go func() {
		var allSismos []alert.Sismo
		for _, cell := range c.gapAnalyzer.GetAllCells() {
			allSismos = append(allSismos, c.gapAnalyzer.GetCellEvents(cell, now.Add(-365*24*time.Hour))...)
		}
		allProjections := alert.ComputeProjections(allSismos, now)

		proj := alert.AnalyzeGridCell(cellID, events, now)
		req := llm.SynthesisRequest{
			TriggerType:             "ANALISIS_MANUAL_TUI",
			FaultName:               alert.GetFaultName(mainshock.Latitude, mainshock.Longitude),
			CellID:                  cellID,
			BValue:                  proj.BValue,
			WeightedEnergy:          proj.WeightedEnergy,
			DynamicRate:             proj.DynamicRate,
			Mainshock:               mainshock,
			RecentEvents:            events,
			Phase:                   targetCell.Phase,
			RecentHistoricalContext: "Análisis manual solicitado por el usuario en la TUI. Analizar panorama tectónico actual, doblete histórico del 24 de junio de 2026 e inestabilidad cortical.",
			AllProjections:          allProjections,
			AllPhases:               snapshot,
			LiveSismos:              events,
			IsManual:                true,
		}
		c.log("Triggering manual Gemma 4 analysis for cell %s...", cellID)
		resp, err := c.gemma.Synthesize(ctx, req)
		if err != nil {
			c.log("Manual Gemma 4 analysis failed: %v", err)
			c.sendTui(tui.MsgGemmaStatus{
				Generating: false,
				Message:    fmt.Sprintf("❌ Error al generar análisis con Gemma 4: %v", err),
			})
			return
		}
		c.sendTui(tui.MsgGemmaReport{Report: resp})
		if pn, ok := c.notifier.(*alert.PushoverNotifier); ok {
			_ = pn.SendSynthesisReport(resp)
		}
	}()
}
