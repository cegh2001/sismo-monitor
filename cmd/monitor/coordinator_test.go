package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/tui"
)

// stubNotifier records Notify calls for assertions.
type stubNotifier struct {
	mu     sync.Mutex
	calls  []alert.Alert
	closed bool
}

func (s *stubNotifier) Notify(ctx context.Context, a alert.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, a)
	return nil
}

func (s *stubNotifier) Start(ctx context.Context) {}

func (s *stubNotifier) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubNotifier) LastCall() (alert.Alert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return alert.Alert{}, false
	}
	return s.calls[len(s.calls)-1], true
}

// seedLockedCell creates a GapAnalyzer whose given cell is locked and contains
// the supplied events. The historical baseline is constructed to ensure the
// cell is recognized as locked:
//   - >= 1 event/month average (we use 36 events 1 year apart, 3 years span)
//   - 90+ days of silence before `now` (latest baseline event is 1 year ago)
func seedLockedCell(t *testing.T, cell string, events []alert.Sismo, now time.Time) *alert.GapAnalyzer {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "seed.json")
	ga := alert.NewGapAnalyzer(dbPath)

	// Historical baseline: 36 events spread across 3 years, 30 days apart,
	// starting 4 years ago. The last baseline event lands ~1 year before
	// `now`, well past the 90-day silence threshold.
	var hist []alert.Sismo
	start := now.AddDate(-4, 0, 0)
	for i := 0; i < 36; i++ {
		hist = append(hist, alert.Sismo{
			ID:        cell + "-hist",
			Source:    "USGS",
			Magnitude: 2.0,
			Time:      start.Add(time.Duration(i) * 30 * 24 * time.Hour),
			GridCell:  cell,
		})
	}
	hist = append(hist, events...)
	ga.SetSismos(hist)
	return ga
}

func TestCoordinatorBuildGapSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// Build a locked cell with a M=5.5 mainshock 3 days ago (ORANGE, not decayed).
	events := []alert.Sismo{
		{ID: "ms", Source: "USGS", Magnitude: 5.5, Time: now.Add(-3 * 24 * time.Hour), GridCell: "G_test"},
	}
	ga := seedLockedCell(t, "G_test", events, now)
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 16)
	coord := NewCoordinator(ga, notif, tuiChan, nil)

	snap := coord.BuildGapSnapshot(now)
	if len(snap) != 1 {
		t.Fatalf("Expected 1 phase entry, got %d", len(snap))
	}
	if snap[0].GridCell != "G_test" {
		t.Errorf("Expected GridCell G_test, got %s", snap[0].GridCell)
	}
	if snap[0].Phase != alert.PhaseReplicas {
		t.Errorf("Expected PhaseReplicas, got %v", snap[0].Phase)
	}
	if snap[0].Decayed {
		t.Errorf("Expected Decayed=false (3d), got true")
	}
}

func TestCoordinatorBuildGapSnapshotNoLockedCells(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	tmpDir := t.TempDir()
	ga := alert.NewGapAnalyzer(filepath.Join(tmpDir, "empty.json"))
	// No sismos -> no locked cells
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 16)
	coord := NewCoordinator(ga, notif, tuiChan, nil)

	snap := coord.BuildGapSnapshot(now)
	if snap == nil {
		t.Error("Expected non-nil empty slice")
	}
	if len(snap) != 0 {
		t.Errorf("Expected 0 entries, got %d", len(snap))
	}
}

func TestCoordinatorEmitGapSnapshotPushesMsgGapState(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	events := []alert.Sismo{
		{ID: "ms", Source: "USGS", Magnitude: 5.5, Time: now.Add(-2 * 24 * time.Hour), GridCell: "G_a"},
	}
	ga := seedLockedCell(t, "G_a", events, now)
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 16)
	coord := NewCoordinator(ga, notif, tuiChan, nil)

	snap := coord.EmitGapSnapshot(context.Background(), now)
	if len(snap) != 1 {
		t.Fatalf("Expected 1 phase entry, got %d", len(snap))
	}
	// Drain the channel
	select {
	case msg := <-tuiChan:
		mgs, ok := msg.(tui.MsgGapState)
		if !ok {
			t.Fatalf("Expected tui.MsgGapState, got %T", msg)
		}
		if len(mgs.Phases) != 1 {
			t.Errorf("Expected 1 phase in MsgGapState, got %d", len(mgs.Phases))
		}
		if mgs.Phases[0].GridCell != "G_a" {
			t.Errorf("Expected cell G_a, got %s", mgs.Phases[0].GridCell)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected MsgGapState within 100ms, got nothing")
	}
}

func TestCoordinatorTransitionTriggersNotifier(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	// Use a 10d-old M4.5 mainshock so we are in ORANGE-decayed state.
	events := []alert.Sismo{
		{ID: "ms", Source: "USGS", Magnitude: 4.5, Time: now.Add(-10 * 24 * time.Hour), GridCell: "G_b"},
	}
	ga := seedLockedCell(t, "G_b", events, now)
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 16)
	coord := NewCoordinator(ga, notif, tuiChan, nil)

	// First emission: prevState is empty (treated as PhaseEstable), so
	// transition to ORANGE must fire the notifier.
	coord.EmitGapSnapshot(context.Background(), now)
	if notif.Count() != 1 {
		t.Fatalf("Expected 1 notifier call on first emission (transition to ORANGE), got %d", notif.Count())
	}
	last, _ := notif.LastCall()
	if last.Level != alert.LevelInstability {
		t.Errorf("Expected LevelInstability, got %v", last.Level)
	}
	if last.Sismo.GridCell != "G_b" {
		t.Errorf("Expected GridCell G_b on alert payload, got %s", last.Sismo.GridCell)
	}

	// Second emission 5 minutes later: same phase, no transition, no extra
	// notifier call (cooldown is also active).
	coord.EmitGapSnapshot(context.Background(), now.Add(5*time.Minute))
	if notif.Count() != 1 {
		t.Errorf("Expected no new notifier call on stable phase, got %d total", notif.Count())
	}
}

func TestCoordinatorCooldownBlocksDuplicateNotifications(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	// Two cells — each transitions to ORANGE on first emit, but the second
	// emit should re-fire on the SECOND cell only (cooldown is per-cell).
	ga1 := seedLockedCell(t, "G_c1", []alert.Sismo{
		{ID: "ms1", Source: "USGS", Magnitude: 4.5, Time: now.Add(-2 * 24 * time.Hour), GridCell: "G_c1"},
	}, now)
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 32)
	coord := NewCoordinator(ga1, notif, tuiChan, nil)

	// Pre-register a second cell (G_c2) by adding events to the same analyzer
	// to simulate multi-cell scenario. We do this by adding events directly.
	_, _ = ga1.Add(alert.Sismo{
		ID:        "ms2",
		Source:    "USGS",
		Magnitude: 4.5,
		Time:      now.Add(-1 * 24 * time.Hour),
		GridCell:  "G_c2",
	})

	// First emit
	coord.EmitGapSnapshot(context.Background(), now)
	callsAfterFirst := notif.Count()

	// Second emit 1 minute later — same cells, same phases, no transitions
	coord.EmitGapSnapshot(context.Background(), now.Add(1*time.Minute))
	callsAfterSecond := notif.Count()
	if callsAfterSecond != callsAfterFirst {
		t.Errorf("Expected cooldown to block new calls within window; got %d -> %d", callsAfterFirst, callsAfterSecond)
	}
}

func TestCoordinatorShouldNotifyTransitionMatrix(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 16)
	coord := NewCoordinator(nil, notif, tuiChan, nil)

	cases := []struct {
		name     string
		prev     alert.SwarmPhase
		curr     alert.SwarmPhase
		expected bool
	}{
		{"GREEN->YELLOW notifies", alert.PhaseEstable, alert.PhaseAtencion, true},
		{"GREEN->RED notifies", alert.PhaseEstable, alert.PhasePrecursor, true},
		{"GREEN->ORANGE notifies", alert.PhaseEstable, alert.PhaseReplicas, true},
		{"GREEN->GRAY silent", alert.PhaseEstable, alert.PhaseSilencio, false},
		{"YELLOW->RED notifies", alert.PhaseAtencion, alert.PhasePrecursor, true},
		{"YELLOW->ORANGE notifies", alert.PhaseAtencion, alert.PhaseReplicas, true},
		{"YELLOW->GREEN silent", alert.PhaseAtencion, alert.PhaseEstable, false},
		{"RED->ORANGE notifies (any->ORANGE)", alert.PhasePrecursor, alert.PhaseReplicas, true},
		{"RED->GREEN silent", alert.PhasePrecursor, alert.PhaseEstable, false},
		{"ORANGE->RED silent (no escalation, cooldown handles)", alert.PhaseReplicas, alert.PhasePrecursor, false},
		{"ORANGE->ORANGE silent (steady)", alert.PhaseReplicas, alert.PhaseReplicas, false},
		{"ORANGE->GREEN silent (decay fade)", alert.PhaseReplicas, alert.PhaseEstable, false},
		{"GRAY->YELLOW notifies (reactivation)", alert.PhaseSilencio, alert.PhaseAtencion, true},
		{"GRAY->ORANGE notifies", alert.PhaseSilencio, alert.PhaseReplicas, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coord.shouldNotifyOnTransition("G_x", tc.prev, tc.curr, now)
			if got != tc.expected {
				t.Errorf("shouldNotifyOnTransition(%v -> %v) = %v, want %v", tc.prev, tc.curr, got, tc.expected)
			}
		})
	}
}

func TestCoordinatorCooldownRespectedForSameCell(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	notif := &stubNotifier{}
	tuiChan := make(chan tea.Msg, 16)
	coord := NewCoordinator(nil, notif, tuiChan, nil)
	coord.cooldown["G_x"] = now.Add(-1 * time.Minute) // 1m ago — still inside 30m cooldown

	// Would normally notify on any->ORANGE, but cooldown blocks.
	if coord.shouldNotifyOnTransition("G_x", alert.PhaseEstable, alert.PhaseReplicas, now) {
		t.Error("Expected cooldown to block notification within 30m window")
	}

	// After 31m, cooldown should release.
	if !coord.shouldNotifyOnTransition("G_x", alert.PhaseEstable, alert.PhaseReplicas, now.Add(31*time.Minute)) {
		t.Error("Expected cooldown to allow notification after 30m")
	}
}

func TestCoordinatorLastMostRelevantEvent(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	events := []alert.Sismo{
		{ID: "small", Magnitude: 2.0, Time: now.Add(-2 * time.Hour)},
		{ID: "big", Magnitude: 5.5, Time: now.Add(-1 * time.Hour)},
		{ID: "med", Magnitude: 3.5, Time: now.Add(-3 * time.Hour)},
	}
	best := lastMostRelevantEvent(events)
	if best.ID != "big" {
		t.Errorf("Expected largest-magnitude event 'big', got %s", best.ID)
	}
	if best.Magnitude != 5.5 {
		t.Errorf("Expected magnitude 5.5, got %v", best.Magnitude)
	}
}

func TestCoordinatorLastMostRelevantEventEmpty(t *testing.T) {
	best := lastMostRelevantEvent(nil)
	if best.ID != "" {
		t.Errorf("Expected zero-value Sismo for empty input, got ID=%s", best.ID)
	}
}
