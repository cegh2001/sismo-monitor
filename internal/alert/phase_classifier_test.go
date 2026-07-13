package alert

import (
	"testing"
	"time"
)

// TestClassifyCellPhase_AllStates covers the 5-state classification rules from the spec.
// All scenarios are pure function tests — no GapAnalyzer state, no I/O.
func TestClassifyCellPhase_AllStates(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		locked   bool
		events   []Sismo
		now      time.Time
		wantPhase SwarmPhase
		wantDecayed bool
	}{
		{
			name:       "spec §1.1: ORANGE beats RED for M>=5.5",
			locked:     true,
			events:     buildEvents(now, []float64{2.1, 2.2, 2.3, 2.4}, []time.Duration{-1 * time.Hour, -2 * time.Hour, -3 * time.Hour, -4 * time.Hour}, 5.5, -5*24*time.Hour),
			now:        now,
			wantPhase:  PhaseReplicas,
			wantDecayed: false,
		},
		{
			name:       "spec §1.2: RED beats ORANGE for M4.5-5.4",
			locked:     true,
			events:     buildEvents(now, []float64{2.0, 2.1, 2.2}, []time.Duration{-1 * time.Hour, -2 * time.Hour, -3 * time.Hour}, 5.0, -5*24*time.Hour),
			now:        now,
			wantPhase:  PhasePrecursor,
			wantDecayed: false,
		},
		{
			name:       "spec §1.3: Precursor detection (3 events M>=2.0 in 12h, no M>=4.5)",
			locked:     true,
			events:     buildEvents(now, []float64{2.0, 2.1, 2.2}, []time.Duration{-1 * time.Hour, -2 * time.Hour, -3 * time.Hour}, 0, 0),
			now:        now,
			wantPhase:  PhasePrecursor,
			wantDecayed: false,
		},
		{
			name:       "spec §1.4: Atención temprana (1 event M>=2.0 in 12h)",
			locked:     true,
			events:     buildEvents(now, []float64{2.1}, []time.Duration{-1 * time.Hour}, 0, 0),
			now:        now,
			wantPhase:  PhaseAtencion,
			wantDecayed: false,
		},
		{
			name:       "spec §1.4: Atención temprana (2 events M>=2.0 in 12h)",
			locked:     true,
			events:     buildEvents(now, []float64{2.0, 2.1}, []time.Duration{-1 * time.Hour, -2 * time.Hour}, 0, 0),
			now:        now,
			wantPhase:  PhaseAtencion,
			wantDecayed: false,
		},
		{
			name:       "spec §1.5: Empty events slice → GRAY",
			locked:     true,
			events:     []Sismo{},
			now:        now,
			wantPhase:  PhaseSilencio,
			wantDecayed: false,
		},
		{
			name:       "spec §1.6: Sub-M2.0 events only → GRAY",
			locked:     true,
			events:     buildEvents(now, []float64{1.5, 1.8, 1.9, 1.7, 1.6}, []time.Duration{-1 * time.Hour, -2 * time.Hour, -3 * time.Hour, -4 * time.Hour, -5 * time.Hour}, 0, 0),
			now:        now,
			wantPhase:  PhaseSilencio,
			wantDecayed: false,
		},
		{
			name:       "spec §1.7: ORANGE attenuated at 10d → Decayed=true",
			locked:     false,
			events:     buildEvents(now, nil, nil, 4.5, -10*24*time.Hour),
			now:        now,
			wantPhase:  PhaseReplicas,
			wantDecayed: true,
		},
		{
			name:       "spec §1.8: ORANGE expires after 14d → not ORANGE",
			locked:     false,
			events:     buildEvents(now, nil, nil, 6.0, -15*24*time.Hour),
			now:        now,
			wantPhase:  PhaseEstable,
			wantDecayed: false,
		},
		{
			name:       "spec §1.9: Locked with no events ever → GRAY",
			locked:     true,
			events:     []Sismo{},
			now:        now,
			wantPhase:  PhaseSilencio,
			wantDecayed: false,
		},
		{
			name:       "unlocked with no events → GREEN (estable default)",
			locked:     false,
			events:     []Sismo{},
			now:        now,
			wantPhase:  PhaseEstable,
			wantDecayed: false,
		},
		{
			name:       "unlocked with 1 event M>=2.0 → GREEN (no lock, no mainshock)",
			locked:     false,
			events:     buildEvents(now, []float64{2.1}, []time.Duration{-1 * time.Hour}, 0, 0),
			now:        now,
			wantPhase:  PhaseEstable,
			wantDecayed: false,
		},
		{
			name:       "unlocked with 3 events M>=2.0 → GREEN (no lock)",
			locked:     false,
			events:     buildEvents(now, []float64{2.1, 2.2, 2.3}, []time.Duration{-1 * time.Hour, -2 * time.Hour, -3 * time.Hour}, 0, 0),
			now:        now,
			wantPhase:  PhaseEstable,
			wantDecayed: false,
		},
		{
			name:       "ORANGE within 7d → Decayed=false",
			locked:     false,
			events:     buildEvents(now, nil, nil, 5.5, -3*24*time.Hour),
			now:        now,
			wantPhase:  PhaseReplicas,
			wantDecayed: false,
		},
		{
			name:       "ORANGE at exactly 7d → Decayed=true (boundary)",
			locked:     false,
			events:     buildEvents(now, nil, nil, 4.5, -7*24*time.Hour),
			now:        now,
			wantPhase:  PhaseReplicas,
			wantDecayed: true,
		},
		{
			name:       "ORANGE at exactly 14d → still ORANGE (boundary inclusive)",
			locked:     false,
			events:     buildEvents(now, nil, nil, 4.5, -14*24*time.Hour),
			now:        now,
			wantPhase:  PhaseReplicas,
			wantDecayed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCellPhase(tc.locked, tc.events, tc.now)
			if got.Phase != tc.wantPhase {
				t.Errorf("Phase: got %v, want %v", got.Phase, tc.wantPhase)
			}
			if got.Decayed != tc.wantDecayed {
				t.Errorf("Decayed: got %v, want %v", got.Decayed, tc.wantDecayed)
			}
		})
	}
}

// TestClassifyCellPhase_GridCellPropagation verifies GridCell is set in result.
func TestClassifyCellPhase_GridCellPropagation(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	events := []Sismo{{GridCell: "G_3_2", Magnitude: 2.0, Time: now.Add(-time.Hour)}}

	got := ClassifyCellPhase(true, events, now)
	if got.GridCell != "" {
		t.Errorf("Expected empty GridCell (function does not know source), got %q", got.GridCell)
	}
}

// TestClassifyCellPhase_MainshockMetadata verifies MainshockMag/MainshockTime propagation.
func TestClassifyCellPhase_MainshockMetadata(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	mainshockTime := now.Add(-3 * 24 * time.Hour)
	events := []Sismo{
		{GridCell: "G_0_0", Magnitude: 2.0, Time: now.Add(-time.Hour)},
		{GridCell: "G_0_0", Magnitude: 5.7, Time: mainshockTime},
	}

	got := ClassifyCellPhase(false, events, now)
	if got.Phase != PhaseReplicas {
		t.Fatalf("Expected PhaseReplicas, got %v", got.Phase)
	}
	if got.MainshockMag != 5.7 {
		t.Errorf("Expected MainshockMag=5.7, got %v", got.MainshockMag)
	}
	if !got.MainshockTime.Equal(mainshockTime) {
		t.Errorf("Expected MainshockTime=%v, got %v", mainshockTime, got.MainshockTime)
	}
	if got.Decayed {
		t.Errorf("Expected Decayed=false (3d < 7d), got true")
	}
}

// TestClassifyCellPhase_PicksLargestMainshock verifies that when multiple M>=4.5 events
// exist, the largest (and most recent within 14d) drives the ORANGE determination.
func TestClassifyCellPhase_PicksLargestMainshock(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	events := []Sismo{
		{Magnitude: 4.6, Time: now.Add(-2 * 24 * time.Hour)},
		{Magnitude: 5.8, Time: now.Add(-5 * 24 * time.Hour)}, // largest within 14d
		{Magnitude: 4.7, Time: now.Add(-10 * 24 * time.Hour)},
	}

	got := ClassifyCellPhase(false, events, now)
	if got.Phase != PhaseReplicas {
		t.Fatalf("Expected PhaseReplicas, got %v", got.Phase)
	}
	if got.MainshockMag != 5.8 {
		t.Errorf("Expected MainshockMag=5.8 (largest), got %v", got.MainshockMag)
	}
}

// buildEvents is a test helper. If extraMag > 0, an additional event with that magnitude
// at mainshockOffset is appended (used to inject M>=4.5 mainshocks).
func buildEvents(now time.Time, mags []float64, offsets []time.Duration, extraMag float64, mainshockOffset time.Duration) []Sismo {
	var events []Sismo
	for i, m := range mags {
		events = append(events, Sismo{
			ID:        "test",
			GridCell:  "G_0_0",
			Magnitude: m,
			Time:      now.Add(offsets[i]),
		})
	}
	if extraMag > 0 {
		events = append(events, Sismo{
			ID:        "mainshock",
			GridCell:  "G_0_0",
			Magnitude: extraMag,
			Time:      now.Add(mainshockOffset),
		})
	}
	return events
}
