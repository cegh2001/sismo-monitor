package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// GapAnalyzer manages seismic gap analysis for Venezuelan grid cells.
type GapAnalyzer struct {
	mu            sync.RWMutex
	sismos        []Sismo
	sismosMap     map[string]int      // Quick O(1) deduplication
	cellSismosMap map[string][]Sismo // Group sismos by grid cell (sorted by Time)
	dbPath        string
	saveChan      chan struct{}
	writerRunning bool                // true if StartWriter background worker is running
}

// NewGapAnalyzer creates a new GapAnalyzer.
func NewGapAnalyzer(dbPath string) *GapAnalyzer {
	return &GapAnalyzer{
		dbPath:        dbPath,
		saveChan:      make(chan struct{}, 1),
		sismosMap:     make(map[string]int),
		cellSismosMap: make(map[string][]Sismo),
	}
}

// Load loads historical sismos from the JSON database.
func (g *GapAnalyzer) Load() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := os.Stat(g.dbPath); os.IsNotExist(err) {
		g.sismos = []Sismo{}
		g.rebuildIndexesLocked()
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
	g.rebuildIndexesLocked()
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
	g.rebuildIndexesLocked()
}

// PurgeSimulationEvents removes all simulation events from the in-memory
// sismos list and rebuilds indexes. This prevents accumulation of synthetic
// test events that cause false-positive instability triggers on repeated runs.
func (g *GapAnalyzer) PurgeSimulationEvents() {
	g.mu.Lock()
	defer g.mu.Unlock()

	var filtered []Sismo
	for _, s := range g.sismos {
		if s.Source != "Simulation" {
			filtered = append(filtered, s)
		}
	}
	g.sismos = filtered
	g.rebuildIndexesLocked()
}

// Add adds a new sismo to the database and evaluates if it triggers a LevelInstability alert.
func (g *GapAnalyzer) Add(s Sismo) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 0. Deduplicate: check if the sismo is already in the database
	if s.ID != "" {
		if idx, found := g.sismosMap[s.ID]; found {
			g.sismos[idx] = s
			g.rebuildIndexesLocked()
			g.saveAsyncLocked()
			return false, nil
		}
	}

	if s.GridCell == "" || s.GridCell == "OUT_OF_BOUNDS" {
		g.sismos = append(g.sismos, s)
		g.cleanupOldSismosLocked(time.Now())
		g.rebuildIndexesLocked()
		g.saveAsyncLocked()
		return false, nil
	}

	// 1. Gather all sismos with Mag >= 2.0 in the 12-hour window ending at s.Time
	cutoff := s.Time.Add(-12 * time.Hour)
	var swarm []Sismo

	if cellSismos, found := g.cellSismosMap[s.GridCell]; found {
		for _, prev := range cellSismos {
			if prev.Magnitude >= 2.0 &&
				(prev.Time.After(cutoff) || prev.Time.Equal(cutoff)) &&
				(prev.Time.Before(s.Time) || prev.Time.Equal(s.Time)) {
				swarm = append(swarm, prev)
			}
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
	g.rebuildIndexesLocked()
	g.saveAsyncLocked()
	return trigger, nil
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

	var locked []string
	for cell := range g.cellSismosMap {
		if g.isLockedAt(cell, now) {
			locked = append(locked, cell)
		}
	}
	return locked
}

// isLockedAt evaluates lock state at time 't' without acquiring locks.
func (g *GapAnalyzer) isLockedAt(gridCell string, t time.Time) bool {
	cellSismos, found := g.cellSismosMap[gridCell]
	if !found {
		return false
	}

	// Filter using binary search for time < t
	idx := sort.Search(len(cellSismos), func(i int) bool {
		return !cellSismos[i].Time.Before(t)
	})

	if idx == 0 {
		return false
	}

	filteredCount := idx
	earliest := cellSismos[0].Time

	span := t.Sub(earliest)
	months := span.Hours() / (24.0 * 30.0)
	if months < 1.0 {
		months = 1.0
	}

	avg := float64(filteredCount) / months
	if avg < 1.0 {
		return false
	}

	// 90 days of silence before t
	silenceCutoff := t.Add(-90 * 24 * time.Hour)
	latestSismoBeforeT := cellSismos[idx-1]
	if latestSismoBeforeT.Time.After(silenceCutoff) || latestSismoBeforeT.Time.Equal(silenceCutoff) {
		return false
	}

	return true
}

// rebuildIndexesLocked maintains index consistency (caller must hold lock).
func (g *GapAnalyzer) rebuildIndexesLocked() {
	// First, sort g.sismos by Time to make binary search on slices possible
	sort.Slice(g.sismos, func(i, j int) bool {
		return g.sismos[i].Time.Before(g.sismos[j].Time)
	})

	g.sismosMap = make(map[string]int)
	g.cellSismosMap = make(map[string][]Sismo)

	for i, s := range g.sismos {
		if s.ID != "" {
			g.sismosMap[s.ID] = i
		}
		if s.GridCell != "" && s.GridCell != "OUT_OF_BOUNDS" {
			g.cellSismosMap[s.GridCell] = append(g.cellSismosMap[s.GridCell], s)
		}
	}
}

// StartWriter runs in the background and coalesces writes to disk.
func (g *GapAnalyzer) StartWriter(ctx context.Context) {
	g.mu.Lock()
	g.writerRunning = true
	g.mu.Unlock()

	cooldown := 50 * time.Millisecond
	var pending bool
	var timerChan <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if pending {
				g.mu.Lock()
				_ = g.saveLocked()
				g.mu.Unlock()
			}
			return

		case <-g.saveChan:
			pending = true
			timerChan = time.After(cooldown)

		case <-timerChan:
			if pending {
				g.mu.Lock()
				_ = g.saveLocked()
				g.mu.Unlock()
				pending = false
				timerChan = nil
			}
		}
	}
}

// saveAsyncLocked handles background save orchestration (caller must hold mu write lock).
func (g *GapAnalyzer) saveAsyncLocked() {
	if g.writerRunning {
		if g.saveChan != nil {
			select {
			case g.saveChan <- struct{}{}:
			default:
			}
		}
	} else {
		_ = g.saveLocked()
	}
}
