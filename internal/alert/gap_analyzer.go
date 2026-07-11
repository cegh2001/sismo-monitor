package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// GapAnalyzer manages seismic gap analysis for Venezuelan grid cells.
type GapAnalyzer struct {
	mu     sync.RWMutex
	sismos []Sismo
	dbPath string
}

// NewGapAnalyzer creates a new GapAnalyzer.
func NewGapAnalyzer(dbPath string) *GapAnalyzer {
	return &GapAnalyzer{
		dbPath: dbPath,
	}
}

// Load loads historical sismos from the JSON database.
func (g *GapAnalyzer) Load() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := os.Stat(g.dbPath); os.IsNotExist(err) {
		g.sismos = []Sismo{}
		return nil
	}

	data, err := os.ReadFile(g.dbPath)
	if err != nil {
		return fmt.Errorf("read db file failed: %w", err)
	}

	var sismos []Sismo
	if err := json.Unmarshal(data, &sismos); err != nil {
		return fmt.Errorf("unmarshal db failed: %w", err)
	}

	g.sismos = sismos
	return nil
}

// Save persists the in-memory sismos to the JSON database.
func (g *GapAnalyzer) Save() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.saveLocked()
}

func (g *GapAnalyzer) saveLocked() error {
	dir := filepath.Dir(g.dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir failed: %w", err)
	}

	// Filter out simulation events to keep the database pure
	var realSismos []Sismo
	for _, s := range g.sismos {
		if s.Source != "Simulation" {
			realSismos = append(realSismos, s)
		}
	}

	data, err := json.MarshalIndent(realSismos, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal db failed: %w", err)
	}

	if err := os.WriteFile(g.dbPath, data, 0644); err != nil {
		return fmt.Errorf("write db file failed: %w", err)
	}
	return nil
}

// SetSismos replaces the internal sismos list (primarily for testing or seed).
func (g *GapAnalyzer) SetSismos(sismos []Sismo) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sismos = sismos
}

// Add adds a new sismo to the database and evaluates if it triggers a LevelInstability alert.
func (g *GapAnalyzer) Add(s Sismo) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 0. Deduplicate: check if the sismo is already in the database
	for i, prev := range g.sismos {
		if prev.ID == s.ID && s.ID != "" {
			g.sismos[i] = s
			err := g.saveLocked()
			return false, err
		}
	}

	if s.GridCell == "" || s.GridCell == "OUT_OF_BOUNDS" {
		g.sismos = append(g.sismos, s)
		g.cleanupOldSismosLocked(time.Now())
		err := g.saveLocked()
		return false, err
	}

	// 1. Gather all sismos with Mag >= 2.0 in the 12-hour window ending at s.Time
	cutoff := s.Time.Add(-12 * time.Hour)
	var swarm []Sismo
	for _, prev := range g.sismos {
		if prev.GridCell == s.GridCell && prev.Magnitude >= 2.0 &&
			(prev.Time.After(cutoff) || prev.Time.Equal(cutoff)) &&
			(prev.Time.Before(s.Time) || prev.Time.Equal(s.Time)) {
			swarm = append(swarm, prev)
		}
	}
	if s.Magnitude >= 2.0 {
		swarm = append(swarm, s)
	}

	trigger := false
	if len(swarm) >= 3 {
		// Find the earliest sismo in the 12-hour swarm window
		earliest := s.Time
		for _, sw := range swarm {
			if sw.Time.Before(earliest) {
				earliest = sw.Time
			}
		}

		// Check if the cell was locked BEFORE the earliest swarm event
		if g.isLockedAt(s.GridCell, earliest) {
			trigger = true
		}
	}

	g.sismos = append(g.sismos, s)
	g.cleanupOldSismosLocked(time.Now())
	err := g.saveLocked()
	return trigger, err
}

func (g *GapAnalyzer) cleanupOldSismosLocked(now time.Time) {
	twoYearsAgo := now.AddDate(-2, 0, 0)
	var keep []Sismo
	for _, s := range g.sismos {
		if s.Time.After(twoYearsAgo) {
			keep = append(keep, s)
		}
	}
	g.sismos = keep
}

// IsLocked checks if a grid cell is currently locked at time 'now'.
func (g *GapAnalyzer) IsLocked(gridCell string, now time.Time) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.isLockedAt(gridCell, now)
}

// GetActiveLockSegments returns locked grid cells at time 'now'.
func (g *GapAnalyzer) GetActiveLockSegments(now time.Time) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	cells := make(map[string]bool)
	for _, s := range g.sismos {
		if s.GridCell != "" && s.GridCell != "OUT_OF_BOUNDS" {
			cells[s.GridCell] = true
		}
	}

	var locked []string
	for cell := range cells {
		if g.isLockedAt(cell, now) {
			locked = append(locked, cell)
		}
	}
	return locked
}

// isLockedAt evaluates lock state at time 't' without acquiring locks.
func (g *GapAnalyzer) isLockedAt(gridCell string, t time.Time) bool {
	var cellSismos []Sismo
	for _, s := range g.sismos {
		if s.GridCell == gridCell && s.Time.Before(t) {
			cellSismos = append(cellSismos, s)
		}
	}

	if len(cellSismos) == 0 {
		return false
	}

	var earliest time.Time
	for _, s := range cellSismos {
		if earliest.IsZero() || s.Time.Before(earliest) {
			earliest = s.Time
		}
	}

	span := t.Sub(earliest)
	months := span.Hours() / (24.0 * 30.0)
	if months < 1.0 {
		months = 1.0
	}

	avg := float64(len(cellSismos)) / months
	if avg < 1.0 {
		return false
	}

	// 90 days of silence before t
	silenceCutoff := t.Add(-90 * 24 * time.Hour)
	for _, s := range cellSismos {
		if s.Time.After(silenceCutoff) || s.Time.Equal(silenceCutoff) {
			return false
		}
	}

	return true
}
