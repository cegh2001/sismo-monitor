package alert

import (
	"sync"
	"time"
)

// ClassifyDanger classifies the danger level based on distance and magnitude.
// If distance > 300.0, the event is classified as LevelInfo (effectively ignored/filtered).
func ClassifyDanger(s Sismo) AlertLevel {
	if s.Distance > 300.0 {
		return LevelInfo
	}
	switch {
	case s.Distance <= 50.0:
		if s.Magnitude >= 5.0 {
			return LevelCritical
		}
		if s.Magnitude >= 3.5 {
			return LevelPreAlert
		}
	case s.Distance <= 150.0:
		if s.Magnitude >= 6.0 {
			return LevelCritical
		}
		if s.Magnitude >= 4.5 {
			return LevelPreAlert
		}
	case s.Distance <= 300.0:
		if s.Magnitude >= 7.0 {
			return LevelCritical
		}
		if s.Magnitude >= 5.5 {
			return LevelPreAlert
		}
	}
	return LevelInfo
}

// SwarmQueue is a thread-safe sliding window queue representing earthquakes
// that occurred under 300 km from La Guaira with magnitude >= 3.0 within a 6-hour window.
type SwarmQueue struct {
	mu     sync.Mutex
	events []Sismo
}

// NewSwarmQueue initializes a new SwarmQueue.
func NewSwarmQueue() *SwarmQueue {
	return &SwarmQueue{
		events: make([]Sismo, 0),
	}
}

// AddAndCheck adds a seismic event to the queue if it's within 300km and magnitude >= 3.0,
// prunes any events older than 6 hours, and returns true if there are >= 5 events.
func (q *SwarmQueue) AddAndCheck(s Sismo) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-6 * time.Hour)

	// Filter out events older than 6 hours
	var filtered []Sismo
	for _, e := range q.events {
		if e.Time.After(cutoff) {
			filtered = append(filtered, e)
		}
	}

	// Add the new event if it is within 300 km of La Guaira and magnitude >= 3.0
	if s.Distance <= 300.0 && s.Magnitude >= 3.0 {
		filtered = append(filtered, s)
	}

	q.events = filtered

	// Trigger Swarm alert if >= 5 events occur in the window
	return len(q.events) >= 5
}

// GetEvents returns a copy of the events currently in the swarm window.
func (q *SwarmQueue) GetEvents() []Sismo {
	q.mu.Lock()
	defer q.mu.Unlock()
	
	res := make([]Sismo, len(q.events))
	copy(res, q.events)
	return res
}
