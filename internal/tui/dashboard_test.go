package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"sismo-monitor/internal/alert"
)

func TestModelUpdate(t *testing.T) {
	updateChan := make(chan tea.Msg, 10)
	model := NewModel(updateChan, "8080")

	t.Run("KeyMsg q or ctrl+c returns tea.Quit", func(t *testing.T) {
		m, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		if cmd == nil {
			t.Fatal("Expected non-nil cmd")
		}
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Errorf("Expected tea.QuitMsg, got %T", msg)
		}
		_ = m
	})

	t.Run("KeyMsg t triggers simulation and updates statusMsg", func(t *testing.T) {
		m, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
		newModel := m.(Model)
		if newModel.statusMsg != "Triggering critical test alert..." {
			t.Errorf("Expected statusMsg 'Triggering critical test alert...', got %q", newModel.statusMsg)
		}
		if cmd == nil {
			t.Error("Expected cmd to trigger simulation, got nil")
		}
	})

	t.Run("KeyMsg s triggers swarm simulation and updates statusMsg", func(t *testing.T) {
		m, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
		newModel := m.(Model)
		if newModel.statusMsg != "Triggering swarm test alerts (5 events)..." {
			t.Errorf("Expected statusMsg 'Triggering swarm test alerts (5 events)...', got %q", newModel.statusMsg)
		}
		if cmd == nil {
			t.Error("Expected cmd to trigger simulation, got nil")
		}
	})

	t.Run("KeyMsg left/right updates sismoScroll statusMsg", func(t *testing.T) {
		m := model
		for i := 0; i < 15; i++ {
			sismo := alert.Sismo{ID: fmt.Sprintf("s-%d", i), Magnitude: 2.0}
			res, _ := m.Update(MsgSismo(sismo))
			m = res.(Model)
		}

		res, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		m = res.(Model)
		if m.sismoScroll != 1 {
			t.Errorf("Expected sismoScroll to be 1, got %d", m.sismoScroll)
		}

		res, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = res.(Model)
		if m.sismoScroll != 0 {
			t.Errorf("Expected sismoScroll to be 0, got %d", m.sismoScroll)
		}
	})

	t.Run("MsgSismo updates Sismos and returns SubscribeToUpdates", func(t *testing.T) {
		sismo := alert.Sismo{
			ID:        "test-sismo",
			Magnitude: 4.5,
			Distance:  120.0,
		}
		m, cmd := model.Update(MsgSismo(sismo))
		newModel := m.(Model)
		if len(newModel.Sismos) != 1 {
			t.Fatalf("Expected 1 sismo, got %d", len(newModel.Sismos))
		}
		if newModel.Sismos[0].ID != "test-sismo" {
			t.Errorf("Expected sismo ID 'test-sismo', got %q", newModel.Sismos[0].ID)
		}
		if cmd == nil {
			t.Error("Expected non-nil cmd")
		}
	})

	t.Run("MsgLog updates Logs and returns SubscribeToUpdates", func(t *testing.T) {
		m, cmd := model.Update(MsgLog("test log message"))
		newModel := m.(Model)
		if len(newModel.Logs) != 1 {
			t.Fatalf("Expected 1 log, got %d", len(newModel.Logs))
		}
		if cmd == nil {
			t.Error("Expected non-nil cmd")
		}
	})

	t.Run("MsgStats updates Stats and statusMsg", func(t *testing.T) {
		stats := MsgStats{
			TotalEvents: 5,
			LocalEvents: 3,
		}
		m, cmd := model.Update(stats)
		newModel := m.(Model)
		if newModel.Stats.TotalEvents != 5 || newModel.Stats.LocalEvents != 3 {
			t.Errorf("Stats not updated, got: %+v", newModel.Stats)
		}
		if newModel.statusMsg != "Stats updated" {
			t.Errorf("Expected statusMsg 'Stats updated', got %q", newModel.statusMsg)
		}
		if cmd == nil {
			t.Error("Expected non-nil cmd")
		}
	})

	t.Run("MsgSismo sorts Sismos slice chronologically", func(t *testing.T) {
		m := model
		now := time.Now()

		s1 := alert.Sismo{ID: "s1", Time: now.Add(-10 * time.Minute)}
		s2 := alert.Sismo{ID: "s2", Time: now}
		s3 := alert.Sismo{ID: "s3", Time: now.Add(-5 * time.Minute)}

		// Inject in out-of-order sequence (s2, s1, s3)
		res, _ := m.Update(MsgSismo(s2))
		m = res.(Model)
		res, _ = m.Update(MsgSismo(s1))
		m = res.(Model)
		res, _ = m.Update(MsgSismo(s3))
		m = res.(Model)

		if len(m.Sismos) != 3 {
			t.Fatalf("Expected 3 sismos, got %d", len(m.Sismos))
		}

		// Should be sorted: s1 (oldest), s3 (middle), s2 (newest)
		if m.Sismos[0].ID != "s1" {
			t.Errorf("Expected first sismo to be s1 (oldest), got %s", m.Sismos[0].ID)
		}
		if m.Sismos[1].ID != "s3" {
			t.Errorf("Expected second sismo to be s3, got %s", m.Sismos[1].ID)
		}
		if m.Sismos[2].ID != "s2" {
			t.Errorf("Expected third sismo to be s2 (newest), got %s", m.Sismos[2].ID)
		}
	})

	t.Run("MsgSismo updates existing entry on duplicate ID", func(t *testing.T) {
		m := model
		now := time.Now()

		s1 := alert.Sismo{ID: "dup-sismo", Magnitude: 4.0, Time: now}
		s1Update := alert.Sismo{ID: "dup-sismo", Magnitude: 4.5, Time: now}

		// Inject original
		res, _ := m.Update(MsgSismo(s1))
		m = res.(Model)
		if len(m.Sismos) != 1 {
			t.Fatalf("Expected 1 sismo, got %d", len(m.Sismos))
		}
		if m.Sismos[0].Magnitude != 4.0 {
			t.Errorf("Expected magnitude 4.0, got %.1f", m.Sismos[0].Magnitude)
		}

		// Inject update
		res, _ = m.Update(MsgSismo(s1Update))
		m = res.(Model)
		if len(m.Sismos) != 1 {
			t.Fatalf("Expected Sismos list length to still be 1, got %d", len(m.Sismos))
		}
		if m.Sismos[0].Magnitude != 4.5 {
			t.Errorf("Expected updated magnitude 4.5, got %.1f", m.Sismos[0].Magnitude)
		}
	})

	t.Run("KeyMsg p toggles currentView between ViewDashboard and ViewPredictive", func(t *testing.T) {
		m := model
		if m.currentView != ViewDashboard {
			t.Errorf("Expected initial view to be ViewDashboard, got %v", m.currentView)
		}

		// Press 'p' to toggle to ViewPredictive
		res, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
		m = res.(Model)
		if m.currentView != ViewPredictive {
			t.Errorf("Expected view to toggle to ViewPredictive, got %v", m.currentView)
		}

		// Press 'p' again to toggle back to ViewDashboard
		res, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
		m = res.(Model)
		if m.currentView != ViewDashboard {
			t.Errorf("Expected view to toggle back to ViewDashboard, got %v", m.currentView)
		}
	})

	t.Run("NewModel computes and caches projections on startup", func(t *testing.T) {
		updateChan := make(chan tea.Msg, 10)
		m := NewModel(updateChan, "8080")
		if len(m.HistoricalSismos) == 0 {
			t.Log("Warning: no historical sismos loaded in test (might be missing file)")
		} else {
			if len(m.Projections) == 0 {
				t.Error("Expected m.Projections to be populated from historical sismos, got 0")
			}
			expectedProjs := alert.ComputeProjections(m.HistoricalSismos, time.Now())
			if len(m.Projections) != len(expectedProjs) {
				t.Errorf("Expected %d projections, got %d", len(expectedProjs), len(m.Projections))
			}
		}
	})

	t.Run("MsgSismo updates cached projections", func(t *testing.T) {
		updateChan := make(chan tea.Msg, 10)
		m := NewModel(updateChan, "8080")

		// Precondition: ensure a unique cell "G_test_cell" is not in Projections cache
		for _, p := range m.Projections {
			if p.GridCell == "G_test_cell" {
				t.Fatal("Precondition failed: G_test_cell already exists in historical projections")
			}
		}

		// Send MsgSismo with new cell
		testSismo := alert.Sismo{
			ID:        "sismo-test-update",
			Source:    "Simulation",
			Magnitude: 5.5,
			Depth:     10.0,
			Latitude:  10.2,
			Longitude: -70.5, // "Falla de Boconó"
			Location:  "Test Cell Location",
			Time:      time.Now(),
			GridCell:  "G_test_cell",
		}

		res, _ := m.Update(MsgSismo(testSismo))
		newModel := res.(Model)

		// Assert that the new cell "G_test_cell" is now in Projections cache
		found := false
		var targetProj alert.FaultProjection
		for _, p := range newModel.Projections {
			if p.GridCell == "G_test_cell" {
				found = true
				targetProj = p
				break
			}
		}

		if !found {
			t.Error("Expected m.Projections to contain the new cell G_test_cell after MsgSismo")
		} else {
			if targetProj.EventCount != 1 {
				t.Errorf("Expected EventCount 1 for test cell, got %d", targetProj.EventCount)
			}
			if targetProj.MainshockMag != 5.5 {
				t.Errorf("Expected MainshockMag 5.5, got %.1f", targetProj.MainshockMag)
			}
		}
	})

	t.Run("MsgSismo updates existing entry on duplicate ID and updates projections cache", func(t *testing.T) {
		updateChan := make(chan tea.Msg, 10)
		m := NewModel(updateChan, "8080")

		now := time.Now()
		s1 := alert.Sismo{
			ID:        "dup-sismo-proj",
			Source:    "Simulation",
			Magnitude: 5.0,
			Depth:     10.0,
			Latitude:  10.2,
			Longitude: -70.5,
			Location:  "Test Cell",
			Time:      now,
			GridCell:  "G_dup_cell",
		}
		s1Update := alert.Sismo{
			ID:        "dup-sismo-proj",
			Source:    "Simulation",
			Magnitude: 5.5,
			Depth:     10.0,
			Latitude:  10.2,
			Longitude: -70.5,
			Location:  "Test Cell Updated",
			Time:      now,
			GridCell:  "G_dup_cell",
		}

		// Inject original
		res, _ := m.Update(MsgSismo(s1))
		m = res.(Model)

		var projBefore alert.FaultProjection
		found := false
		for _, p := range m.Projections {
			if p.GridCell == "G_dup_cell" {
				projBefore = p
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Expected G_dup_cell in projections after first insert")
		}
		if projBefore.MainshockMag != 5.0 {
			t.Errorf("Expected initial MainshockMag 5.0, got %.1f", projBefore.MainshockMag)
		}

		// Inject update
		res, _ = m.Update(MsgSismo(s1Update))
		m = res.(Model)

		var projAfter alert.FaultProjection
		found = false
		for _, p := range m.Projections {
			if p.GridCell == "G_dup_cell" {
				projAfter = p
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Expected G_dup_cell in projections after update")
		}
		if projAfter.MainshockMag != 5.5 {
			t.Errorf("Expected updated MainshockMag 5.5, got %.1f", projAfter.MainshockMag)
		}
	})
}
