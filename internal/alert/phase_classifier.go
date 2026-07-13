package alert

import (
	"encoding/json"
	"time"
)

// SwarmPhase represents the operational phase of a locked grid cell, derived
// from event history. It encodes both the threat level and the visual color
// the TUI must render.
type SwarmPhase int

const (
	// PhaseEstable (GREEN) — default: no active swarm, no recent mainshock,
	// or cell is not locked. Lowest priority.
	PhaseEstable SwarmPhase = iota
	// PhaseSilencio (GRAY) — locked cell with no qualifying activity in the
	// 30-day window. Seismic silence, not a critical alert.
	PhaseSilencio
	// PhaseAtencion (YELLOW) — locked cell with 1-2 events M>=2.0 in 12h.
	// Early warning; precursor watch.
	PhaseAtencion
	// PhasePrecursor (RED) — locked cell with >=3 events M>=2.0 in 12h.
	// Active swarm pattern inside a previously-quiet locked segment.
	PhasePrecursor
	// PhaseReplicas (ORANGE) — most recent M>=4.5 event within 14 days.
	// Dominant over RED for M>=5.5; subordinate to RED for M4.5-5.4.
	PhaseReplicas
)

// CellPhase bundles the classification result for a single grid cell. It is
// produced by ClassifyCellPhase and consumed by both the TUI (for rendering)
// and the coordinator (for transition detection + notification).
type CellPhase struct {
	GridCell      string
	Phase         SwarmPhase
	Decayed       bool      // true when PhaseReplicas 7-14d since mainshock
	MainshockMag  float64
	MainshockTime time.Time
}

// MarshalJSON implements custom encoding so that an unset (zero) CellPhase
// serializes to null. This lets FaultProjection.Phase use `omitempty` and
// keeps the JSON payload clean when no phase has been classified yet.
func (c CellPhase) MarshalJSON() ([]byte, error) {
	if c.Phase == PhaseEstable && c.MainshockMag == 0 && c.MainshockTime.IsZero() {
		return []byte("null"), nil
	}
	// Wrap into a private alias to avoid infinite recursion.
	type cellPhaseAlias CellPhase
	return json.Marshal(cellPhaseAlias(c))
}

// UnmarshalJSON tolerates both object form and null (zero value).
func (c *CellPhase) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = CellPhase{}
		return nil
	}
	type cellPhaseAlias CellPhase
	return json.Unmarshal(data, (*cellPhaseAlias)(c))
}

// ClassifyCellPhase is a pure function: same inputs always produce the same
// CellPhase. The coordinator calls it per locked cell per event cycle.
//
// Rules (priority order — first match wins):
//  1. Most recent M>=4.5 within 14 days → PhaseReplicas (ORANGE).
//     - If 7-14d ago: Decayed=true.
//     - ORANGE wins over RED when MainshockMag >= 5.5.
//     - RED wins over ORANGE when MainshockMag is in [4.5, 5.4).
//  2. Locked + >=3 events M>=2.0 in last 12h → PhasePrecursor (RED).
//  3. Locked + 1-2 events M>=2.0 in last 12h → PhaseAtencion (YELLOW).
//  4. Locked + 0 events with M>=2.0 in last 30d → PhaseSilencio (GRAY).
//  5. Unlocked → PhaseEstable (GREEN).
//
// Events with M<2.0 never count toward the 12h/30d activity checks. An empty
// events slice always returns PhaseSilencio for locked cells.
func ClassifyCellPhase(locked bool, events []Sismo, now time.Time) CellPhase {
	result := CellPhase{Phase: PhaseEstable}

	// Rule 1: ORANGE — most recent M>=4.5 within 14 days
	var mainshock Sismo
	hasMainshock := false
	for _, s := range events {
		if s.Magnitude >= 4.5 {
			if !hasMainshock || s.Magnitude > mainshock.Magnitude {
				mainshock = s
				hasMainshock = true
			}
		}
	}

	if hasMainshock {
		age := now.Sub(mainshock.Time)
		if age >= 0 && age <= 14*24*time.Hour {
			result.Phase = PhaseReplicas
			result.MainshockMag = mainshock.Magnitude
			result.MainshockTime = mainshock.Time
			if age >= 7*24*time.Hour {
				result.Decayed = true
			}
			// ORANGE wins when M>=5.5 (per spec §1.1); RED wins for M4.5-5.4 (per spec §1.2).
			if mainshock.Magnitude < 5.5 {
				// Check RED condition: locked + >=3 events M>=2.0 in 12h
				if locked && countM2PlusIn12h(events, now) >= 3 {
					result.Phase = PhasePrecursor
					// Keep Mainshock fields populated for downstream consumers
				}
			}
			return result
		}
	}

	// Rule 2: RED — locked + >=3 events M>=2.0 in 12h
	if locked {
		count12 := countM2PlusIn12h(events, now)
		switch {
		case count12 >= 3:
			result.Phase = PhasePrecursor
		case count12 >= 1:
			result.Phase = PhaseAtencion
		default:
			// Rule 3/4: locked but no M>=2.0 activity — check 30-day silence vs unlocked default
			result.Phase = PhaseSilencio
		}
	}

	return result
}

// countM2PlusIn12h returns the number of events with Magnitude >= 2.0 occurring
// in the (now-12h, now] window. Used to detect swarm activity for RED/YELLOW
// classification. Excludes events with M < 2.0 (they don't trigger precursors).
func countM2PlusIn12h(events []Sismo, now time.Time) int {
	cutoff := now.Add(-12 * time.Hour)
	count := 0
	for _, s := range events {
		if s.Magnitude < 2.0 {
			continue
		}
		if (s.Time.After(cutoff) || s.Time.Equal(cutoff)) && (s.Time.Before(now) || s.Time.Equal(now)) {
			count++
		}
	}
	return count
}
