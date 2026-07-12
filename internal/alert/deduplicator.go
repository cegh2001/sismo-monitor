package alert

import (
	"context"
	"strings"
	"sync"
	"time"

	"sismo-monitor/internal/geo"
)

// sourcePriority tracks which sources are preferred for magnitude data.
var magnitudeSources = map[string]bool{
	"USGS": true,
	"EMSC": true,
}

// sourceHasAny returns true if src contains any of the target sources
// (accounting for fused "+" separators).
func sourceHasAny(src string, targets map[string]bool) bool {
	for _, part := range strings.Split(src, "+") {
		if targets[part] {
			return true
		}
	}
	return false
}

// Deduplicator manages sliding window space-time deduplication of seismic events.
type Deduplicator struct {
	mu           sync.Mutex
	window       time.Duration
	maxDistance  float64
	recentEvents []Sismo
}

// NewDeduplicator creates a new Deduplicator with a given temporal window and spatial distance.
func NewDeduplicator(window time.Duration, maxDistance float64) *Deduplicator {
	return &Deduplicator{
		window:       window,
		maxDistance:  maxDistance,
		recentEvents: make([]Sismo, 0),
	}
}

// Add processes an incoming event. If it matches a recently processed event (within window and maxDistance),
// it fuses the two events and returns the fused event with isUpdate = true.
// Otherwise, it registers the event as new and returns it with isUpdate = false.
//
// Simulation events bypass deduplication entirely — they are synthetic test events
// that should never interfere with real event fusion or with each other.
func (d *Deduplicator) Add(s Sismo) (Sismo, bool) {
	if s.Source == "Simulation" {
		return s, false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Clean up events older than 10 minutes relative to the current time to avoid memory growth
	now := time.Now()
	cutoff := now.Add(-10 * time.Minute)
	n := 0
	for _, ev := range d.recentEvents {
		if ev.Time.After(cutoff) {
			d.recentEvents[n] = ev
			n++
		}
	}
	d.recentEvents = d.recentEvents[:n]

	// Check if s matches any event in our recentEvents list
	for i, prev := range d.recentEvents {
		timeDiff := prev.Time.Sub(s.Time)
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		if timeDiff <= d.window {
			dist := geo.HaversineDistance(prev.Latitude, prev.Longitude, s.Latitude, s.Longitude)
			if dist < d.maxDistance {
				// Match found! Fuse them.
				fused := d.fuse(prev, s)
				d.recentEvents[i] = fused
				return fused, true
			}
		}
	}

	// No match. Add to recentEvents and return as new.
	d.recentEvents = append(d.recentEvents, s)
	return s, false
}

// GetRecentEvents returns a copy of the recent events buffer.
func (d *Deduplicator) GetRecentEvents() []Sismo {
	d.mu.Lock()
	defer d.mu.Unlock()
	res := make([]Sismo, len(d.recentEvents))
	copy(res, d.recentEvents)
	return res
}

// StartCleanup runs a background goroutine that periodically prunes stale events
// from the recentEvents buffer. This prevents memory accumulation when no new
// events arrive for extended periods.
func (d *Deduplicator) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			n := 0
			for _, ev := range d.recentEvents {
				if ev.Time.After(cutoff) {
					d.recentEvents[n] = ev
					n++
				}
			}
			d.recentEvents = d.recentEvents[:n]
			d.mu.Unlock()
		}
	}
}

// fuse merges prev (already processed) and curr (newly arrived) Sismo.
// It prioritizes Funvisis for location/depth and USGS/EMSC for magnitude.
func (d *Deduplicator) fuse(prev, curr Sismo) Sismo {
	fused := prev

	// 1. Location/Depth/Coordinates: Prefer Funvisis
	if curr.Source == "Funvisis" && prev.Source != "Funvisis" {
		fused.Latitude = curr.Latitude
		fused.Longitude = curr.Longitude
		fused.Depth = curr.Depth
		fused.Location = curr.Location
		fused.Distance = curr.Distance
		fused.GridCell = curr.GridCell
	}

	// 2. Magnitude: Prefer global sources (USGS, EMSC)
	currHasMag := sourceHasAny(curr.Source, magnitudeSources)
	prevHasMag := sourceHasAny(prev.Source, magnitudeSources)

	if currHasMag && !prevHasMag {
		fused.Magnitude = curr.Magnitude
	} else if currHasMag && prevHasMag {
		if curr.Source == "USGS" {
			fused.Magnitude = curr.Magnitude
		}
	}

	// 3. Keep the earliest time
	if curr.Time.Before(prev.Time) {
		fused.Time = curr.Time
	}

	// 4. Combine sources in the source field
	if !strings.Contains(prev.Source, curr.Source) {
		fused.Source = prev.Source + "+" + curr.Source
	}

	return fused
}
