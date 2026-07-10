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
}

// AlertLevel represents the danger classification of a seismic alert.
type AlertLevel string

const (
	LevelInfo     AlertLevel = "INFO"
	LevelPreAlert AlertLevel = "PRE_ALERT"
	LevelCritical AlertLevel = "CRITICAL"
	LevelSwarm    AlertLevel = "SWARM"
)

// Alert wraps a seismic event with its evaluated threat level.
type Alert struct {
	Sismo Sismo      `json:"sismo"`
	Level AlertLevel `json:"level"`
}
