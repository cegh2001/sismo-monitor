package alert

import (
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
