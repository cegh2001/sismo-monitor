package alert

import (
	"time"
)

// Sismo represents the structured representation of a seismic event.
type Sismo struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"` // "EMSC" | "Funvisis" | "Simulation"
	Magnitude float64   `json:"magnitude"`
	Depth     float64   `json:"depth"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Location  string    `json:"location"`
	Time      time.Time `json:"time"`
	Distance  float64   `json:"distance"` // Distance to La Guaira in km
	GridCell  string    `json:"grid_cell"`
}

// PWaveTravelTime returns the estimated travel time of the P-wave to La Guaira.
func (s Sismo) PWaveTravelTime() time.Duration {
	if s.Distance <= 0 {
		return 0
	}
	return time.Duration(s.Distance / 6.0 * float64(time.Second))
}

// SWaveTravelTime returns the estimated travel time of the S-wave to La Guaira.
func (s Sismo) SWaveTravelTime() time.Duration {
	if s.Distance <= 0 {
		return 0
	}
	return time.Duration(s.Distance / 3.5 * float64(time.Second))
}

// PWaveArrivalTime returns the estimated arrival time of the P-wave at La Guaira.
func (s Sismo) PWaveArrivalTime() time.Time {
	return s.Time.Add(s.PWaveTravelTime())
}

// SWaveArrivalTime returns the estimated arrival time of the S-wave at La Guaira.
func (s Sismo) SWaveArrivalTime() time.Time {
	return s.Time.Add(s.SWaveTravelTime())
}

// AlertLevel represents the danger classification of a seismic alert.
type AlertLevel string

const (
	LevelInfo        AlertLevel = "INFO"
	LevelPreAlert    AlertLevel = "PRE_ALERT"
	LevelCritical    AlertLevel = "CRITICAL"
	LevelSwarm       AlertLevel = "SWARM"
	LevelInstability AlertLevel = "INSTABILITY"
)

// Alert wraps a seismic event with its evaluated threat level.
type Alert struct {
	Sismo        Sismo      `json:"sismo"`
	Level        AlertLevel `json:"level"`
	EarlyWarning bool       `json:"early_warning,omitempty"` // when true, notifier uses [EARLY WARNING] format
	Body         string     `json:"body,omitempty"`          // overrides default body when EarlyWarning is true
}

