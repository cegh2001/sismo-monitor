package alert

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestGapAnalyzerLockedAndTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sismos_historicos.json")

	analyzer := NewGapAnalyzer(dbPath)

	// We define 'now' as our reference time.
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// Seed historical data:
	// We want to average >= 1 sismo/month.
	// Let's create 24 sismos in grid cell "G_0_0", one every 15 days, starting 2 years ago, and ending 100 days ago.
	var historical []Sismo
	startDate := now.AddDate(-2, 0, 0)
	endDate := now.Add(-100 * 24 * time.Hour)

	for date := startDate; date.Before(endDate); date = date.Add(15 * 24 * time.Hour) {
		historical = append(historical, Sismo{
			ID:        "hist",
			Source:    "USGS",
			Magnitude: 2.5,
			Depth:     10.0,
			Latitude:  10.0,
			Longitude: -67.0,
			Location:  "Venezuela Test Location",
			Time:      date,
			Distance:  100.0,
			GridCell:  "G_0_0",
		})
	}

	analyzer.SetSismos(historical)

	// Verify grid cell "G_0_0" is locked at time 'now'.
	// Since the last historical event was 100 days ago, it has 0 events in the last 90 days.
	lockedCells := analyzer.GetActiveLockSegments(now)
	found := false
	for _, c := range lockedCells {
		if c == "G_0_0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Expected grid cell 'G_0_0' to be locked, but it wasn't. Active gaps: %v", lockedCells)
	}

	// Now inject events in 'now' window and check for instability trigger.
	// Swarm requirement: >= 3 events with Mag >= 2.0 in 12h window inside a locked segment.
	e1 := Sismo{
		ID:        "event-1",
		Source:    "Simulation",
		Magnitude: 2.1,
		Depth:     10.0,
		Latitude:  10.0,
		Longitude: -67.0,
		Location:  "Venezuela Test Location",
		Time:      now.Add(1 * time.Hour),
		Distance:  100.0,
		GridCell:  "G_0_0",
	}

	e2 := Sismo{
		ID:        "event-2",
		Source:    "Simulation",
		Magnitude: 2.2,
		Depth:     10.0,
		Latitude:  10.0,
		Longitude: -67.0,
		Location:  "Venezuela Test Location",
		Time:      now.Add(2 * time.Hour),
		Distance:  100.0,
		GridCell:  "G_0_0",
	}

	e3 := Sismo{
		ID:        "event-3",
		Source:    "Simulation",
		Magnitude: 2.3,
		Depth:     10.0,
		Latitude:  10.0,
		Longitude: -67.0,
		Location:  "Venezuela Test Location",
		Time:      now.Add(3 * time.Hour),
		Distance:  100.0,
		GridCell:  "G_0_0",
	}

	// Add event 1. Should not trigger instability.
	trigger1, err := analyzer.Add(e1)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if trigger1 {
		t.Errorf("Event 1 should not have triggered instability")
	}

	// Add event 2. Should not trigger instability.
	trigger2, err := analyzer.Add(e2)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if trigger2 {
		t.Errorf("Event 2 should not have triggered instability")
	}

	// Add event 3. Should trigger instability!
	trigger3, err := analyzer.Add(e3)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !trigger3 {
		t.Errorf("Event 3 should have triggered instability")
	}
}

func TestGapAnalyzerDuplicateUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sismos_historicos.json")

	analyzer := NewGapAnalyzer(dbPath)

	now := time.Now()
	s := Sismo{
		ID:        "test-dup-1",
		Source:    "EMSC",
		Magnitude: 4.0,
		Latitude:  10.0,
		Longitude: -67.0,
		Time:      now,
		GridCell:  "G_0_0",
	}

	_, err := analyzer.Add(s)
	if err != nil {
		t.Fatalf("Failed to add sismo: %v", err)
	}

	s.Magnitude = 4.5
	s.Source = "EMSC+Funvisis"
	_, err = analyzer.Add(s)
	if err != nil {
		t.Fatalf("Failed to add duplicate sismo: %v", err)
	}

	analyzer2 := NewGapAnalyzer(dbPath)
	err = analyzer2.Load()
	if err != nil {
		t.Fatalf("Failed to load analyzer: %v", err)
	}

	sismos := analyzer2.sismos
	if len(sismos) != 1 {
		t.Fatalf("Expected 1 sismo in database, got %d", len(sismos))
	}

	if sismos[0].Magnitude != 4.5 {
		t.Errorf("Expected updated magnitude 4.5, got %.1f", sismos[0].Magnitude)
	}

	if sismos[0].Source != "EMSC+Funvisis" {
		t.Errorf("Expected updated source 'EMSC+Funvisis', got %q", sismos[0].Source)
	}
}

func TestGapAnalyzerBackgroundWriter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sismos_historicos_bg.json")

	analyzer := NewGapAnalyzer(dbPath)
	go analyzer.StartWriter(ctx)

	// Since we are writing asynchronously, Add should write to saveChan.
	s := Sismo{
		ID:        "test-bg-1",
		Source:    "EMSC",
		Magnitude: 3.5,
		Latitude:  10.0,
		Longitude: -67.0,
		Time:      time.Now(),
		GridCell:  "G_0_0",
	}

	_, err := analyzer.Add(s)
	if err != nil {
		t.Fatalf("Failed to add sismo: %v", err)
	}

	// Wait up to 200ms for background writer to run (since it coalesces updates).
	time.Sleep(150 * time.Millisecond)

	analyzer2 := NewGapAnalyzer(dbPath)
	err = analyzer2.Load()
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	if len(analyzer2.sismos) != 1 {
		t.Errorf("Expected 1 sismo, got %d", len(analyzer2.sismos))
	}
}

func TestGapAnalyzerBackgroundWriterCoalesce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sismos_historicos_coalesce.json")

	analyzer := NewGapAnalyzer(dbPath)
	go analyzer.StartWriter(ctx)

	// Add 5 sismos in rapid succession.
	for i := 0; i < 5; i++ {
		s := Sismo{
			ID:        fmt.Sprintf("coalesce-%d", i),
			Source:    "EMSC",
			Magnitude: 3.0 + float64(i)*0.1,
			Latitude:  10.0,
			Longitude: -67.0,
			Time:      time.Now().Add(time.Duration(i) * time.Second),
			GridCell:  "G_0_0",
		}
		_, err := analyzer.Add(s)
		if err != nil {
			t.Fatalf("Failed to add sismo: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Wait 150ms for coalescing to complete and the background writer to save to disk.
	time.Sleep(150 * time.Millisecond)

	analyzer2 := NewGapAnalyzer(dbPath)
	err := analyzer2.Load()
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	if len(analyzer2.sismos) != 5 {
		t.Errorf("Expected 5 sismos to be saved, got %d", len(analyzer2.sismos))
	}
}

func TestGapAnalyzerPurgeSimulationEvents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sismos_historicos.json")

	analyzer := NewGapAnalyzer(dbPath)

	now := time.Now()

	// Seed with a mix of real and simulation events
	realSismos := []Sismo{
		{ID: "real-1", Source: "USGS", Magnitude: 3.0, Latitude: 10.0, Longitude: -67.0, Time: now.AddDate(0, -6, 0), GridCell: "G_0_0"},
		{ID: "real-2", Source: "EMSC", Magnitude: 3.5, Latitude: 10.0, Longitude: -67.0, Time: now.AddDate(0, -5, 0), GridCell: "G_0_0"},
		{ID: "real-3", Source: "Funvisis", Magnitude: 2.8, Latitude: 10.0, Longitude: -67.0, Time: now.AddDate(0, -4, 0), GridCell: "G_0_0"},
		{ID: "real-4", Source: "USGS", Magnitude: 4.0, Latitude: 10.5, Longitude: -66.0, Time: now.AddDate(0, -3, 0), GridCell: "G_1_1"},
	}
	simSismos := []Sismo{
		{ID: "sim-1", Source: "Simulation", Magnitude: 2.5, Latitude: 10.0, Longitude: -67.0, Time: now.Add(-1 * time.Hour), GridCell: "G_0_0"},
		{ID: "sim-2", Source: "Simulation", Magnitude: 3.0, Latitude: 10.0, Longitude: -67.0, Time: now.Add(-30 * time.Minute), GridCell: "G_0_0"},
		{ID: "sim-3", Source: "Simulation", Magnitude: 2.0, Latitude: 10.5, Longitude: -66.0, Time: now.Add(-15 * time.Minute), GridCell: "G_1_1"},
	}

	all := append(append([]Sismo{}, realSismos...), simSismos...)
	analyzer.SetSismos(all)

	// Verify preconditions: all 7 events are present
	if len(analyzer.sismos) != 7 {
		t.Fatalf("Expected 7 sismos before purge, got %d", len(analyzer.sismos))
	}

	// Verify G_0_0 cell exists with all events (3 real + 2 sim = 5)
	cellSismos := analyzer.cellSismosMap["G_0_0"]
	if len(cellSismos) != 5 {
		t.Fatalf("Expected 5 sismos in G_0_0 before purge, got %d", len(cellSismos))
	}

	// Purge
	analyzer.PurgeSimulationEvents()

	// Verify only real sismos remain (4 total)
	if len(analyzer.sismos) != 4 {
		t.Errorf("Expected 4 sismos after purge, got %d", len(analyzer.sismos))
	}

	// Verify all remaining sismos are non-simulation
	for _, s := range analyzer.sismos {
		if s.Source == "Simulation" {
			t.Errorf("Found simulation event %q still present after purge", s.ID)
		}
	}

	// Verify G_0_0 cell now has only 3 events (the 3 real ones)
	cellSismos = analyzer.cellSismosMap["G_0_0"]
	if len(cellSismos) != 3 {
		t.Errorf("Expected 3 sismos in G_0_0 after purge, got %d", len(cellSismos))
	}

	// Verify G_1_1 cell now has only 1 event
	cellSismos = analyzer.cellSismosMap["G_1_1"]
	if len(cellSismos) != 1 {
		t.Errorf("Expected 1 sismo in G_1_1 after purge, got %d", len(cellSismos))
	}

	// Verify sismosMap is rebuilt: real IDs must exist, sim IDs must not
	if _, found := analyzer.sismosMap["real-1"]; !found {
		t.Error("Expected real-1 to exist in sismosMap after purge")
	}
	if _, found := analyzer.sismosMap["sim-1"]; found {
		t.Error("Expected sim-1 to be removed from sismosMap after purge")
	}
}

