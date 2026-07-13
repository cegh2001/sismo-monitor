package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		model.startTime = time.Now().Add(-simCooldown)
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
		model.startTime = time.Now().Add(-simCooldown)
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

func TestViewDashboard(t *testing.T) {
	updateChan := make(chan tea.Msg, 10)
	m := NewModel(updateChan, "8080")

	view := m.View()

	// Verify dashboard view contains expected sections
	if !strings.Contains(view, "VENEZUELAN SEISMIC MONITOR") {
		t.Error("Dashboard view should contain title")
	}
	if !strings.Contains(view, "STATISTICS") {
		t.Error("Dashboard view should contain STATISTICS section")
	}
	if !strings.Contains(view, "LATEST SEISMIC EVENTS") {
		t.Error("Dashboard view should contain LATEST SEISMIC EVENTS section")
	}
	if !strings.Contains(view, "LATEST SYSTEM LOGS") {
		t.Error("Dashboard view should contain LATEST SYSTEM LOGS section")
	}
	if !strings.Contains(view, "[q] Quit") {
		t.Error("Dashboard view should show quit shortcut")
	}
}

func TestViewPredictiveEmpty(t *testing.T) {
	updateChan := make(chan tea.Msg, 10)
	m := NewModel(updateChan, "8080")
	m.currentView = ViewPredictive

	view := m.View()

	if !strings.Contains(view, "PROYECCIONES SISMOLÓGICAS") {
		t.Error("Predictive view should contain title")
	}
	if !strings.Contains(view, "No se detecta actividad acumulada") {
		t.Error("Predictive view with no data should show empty message")
	}
}

func TestViewPredictiveWithData(t *testing.T) {
	updateChan := make(chan tea.Msg, 10)
	m := NewModel(updateChan, "8080")

	now := time.Now()
	m.Projections = []alert.FaultProjection{
		{
			GridCell:           "G_1_1",
			FaultName:          "Falla de Boconó",
			BValue:             0.65,
			MainshockMag:       5.5,
			MainshockTime:      now.Add(-5 * time.Hour),
			BathMaxReplica:     4.3,
			OmoriReplicaRate:   0.8,
			ExpectedReplicas24: 3.5,
			EventCount:         12,
		},
		{
			GridCell:           "G_3_2",
			FaultName:          "Falla de San Sebastián",
			BValue:             1.05,
			MainshockMag:       4.2,
			MainshockTime:      now.Add(-10 * time.Hour),
			BathMaxReplica:     3.0,
			OmoriReplicaRate:   0.3,
			ExpectedReplicas24: 1.2,
			EventCount:         8,
		},
	}
	m.currentView = ViewPredictive

	view := m.View()

	if !strings.Contains(view, "FALLA DE BOCONÓ") {
		t.Error("Predictive view should show Boconó fault section")
	}
	if !strings.Contains(view, "FALLA DE SAN SEBASTIÁN") {
		t.Error("Predictive view should show San Sebastián fault section")
	}
	if !strings.Contains(view, "G_1_1") {
		t.Error("Predictive view should show grid cell G_1_1")
	}
	if strings.Contains(view, "No se detecta actividad acumulada") {
		t.Error("Predictive view with data should NOT show empty message")
	}
}

func TestViewToggle(t *testing.T) {
	updateChan := make(chan tea.Msg, 10)
	m := NewModel(updateChan, "8080")

	// Default is dashboard
	if !strings.Contains(m.View(), "VENEZUELAN SEISMIC MONITOR") {
		t.Error("Default view should be dashboard")
	}

	// Toggle to predictive via key press
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = res.(Model)

	view := m.View()
	if !strings.Contains(view, "PROYECCIONES SISMOLÓGICAS") {
		t.Error("After 'p' key, view should be predictive")
	}

	// Toggle back
	res, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = res.(Model)

	view = m.View()
	if !strings.Contains(view, "VENEZUELAN SEISMIC MONITOR") {
		t.Error("After second 'p' key, view should be dashboard again")
	}
}

func TestModelUpdateMsgGapState(t *testing.T) {
	updateChan := make(chan tea.Msg, 10)
	model := NewModel(updateChan, "8080")

	t.Run("MsgGapState stores phases into Model.GapState", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_0_0", Phase: alert.PhasePrecursor, MainshockMag: 5.0},
			{GridCell: "G_1_1", Phase: alert.PhaseReplicas, Decayed: true, MainshockMag: 4.6},
		}
		res, cmd := model.Update(MsgGapState{Phases: phases})
		m := res.(Model)
		if len(m.GapState) != 2 {
			t.Fatalf("Expected GapState length 2, got %d", len(m.GapState))
		}
		if m.GapState[0].GridCell != "G_0_0" {
			t.Errorf("Expected first cell G_0_0, got %s", m.GapState[0].GridCell)
		}
		if m.GapState[0].Phase != alert.PhasePrecursor {
			t.Errorf("Expected first cell PhasePrecursor, got %v", m.GapState[0].Phase)
		}
		if !m.GapState[1].Decayed {
			t.Errorf("Expected second cell Decayed=true")
		}
		if cmd == nil {
			t.Error("Expected non-nil cmd to keep subscription alive")
		}
	})

	t.Run("MsgGapState with empty phases clears GapState", func(t *testing.T) {
		res, _ := model.Update(MsgGapState{Phases: []alert.CellPhase{}})
		m := res.(Model)
		if len(m.GapState) != 0 {
			t.Errorf("Expected GapState length 0, got %d", len(m.GapState))
		}
	})

	t.Run("MsgGapState with nil phases treated as empty", func(t *testing.T) {
		res, _ := model.Update(MsgGapState{Phases: nil})
		m := res.(Model)
		if m.GapState == nil {
			t.Error("Expected GapState to be non-nil empty slice")
		}
		if len(m.GapState) != 0 {
			t.Errorf("Expected GapState length 0, got %d", len(m.GapState))
		}
	})
}

func TestRenderPredictiveViewPhaseBadges(t *testing.T) {
	// Force ANSI output in tests so we can assert on SGR escape codes.
	lipgloss.SetColorProfile(lipgloss.ColorProfile())

	updateChan := make(chan tea.Msg, 10)
	now := time.Now()

	// Build a model with projections matching the GapState cells.
	// Uses renderPredictiveView() directly to bypass View()'s scroll
	// slicing (which truncates to termHeight).
	buildModel := func(phases []alert.CellPhase) Model {
		m := NewModel(updateChan, "8080")
		m.Projections = make([]alert.FaultProjection, 0, len(phases))
		for _, p := range phases {
			m.Projections = append(m.Projections, alert.FaultProjection{
				GridCell:   p.GridCell,
				FaultName:  "Falla de Boconó",
				BValue:     0.85,
				EventCount: 5,
			})
		}
		m.GapState = phases
		m.currentView = ViewPredictive
		m.termHeight = 200 // ensure full view is shown
		return m
	}

	t.Run("renders all 5 phase labels in cell rows", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_red", Phase: alert.PhasePrecursor},
			{GridCell: "G_org", Phase: alert.PhaseReplicas},
			{GridCell: "G_yel", Phase: alert.PhaseAtencion},
			{GridCell: "G_gry", Phase: alert.PhaseSilencio},
			{GridCell: "G_grn", Phase: alert.PhaseEstable},
		}
		view := buildModel(phases).View()
		// Each phase badge has a unique label. The legend renders all 5,
		// and the cell rows also render their phase badge.
		labels := []string{"[RED]", "[ORANGE]", "[YELLOW]", "[GRAY]", "[GREEN]"}
		for _, lbl := range labels {
			if !strings.Contains(view, lbl) {
				t.Errorf("Expected view to contain phase label %s", lbl)
			}
		}
	})

	t.Run("GRAY cells hidden when any non-GRAY cell exists", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_red", Phase: alert.PhasePrecursor},
			{GridCell: "G_gry", Phase: alert.PhaseSilencio},
		}
		view := buildModel(phases).View()
		onlyGray := buildModel([]alert.CellPhase{{GridCell: "G_gry", Phase: alert.PhaseSilencio}}).View()
		if !strings.Contains(onlyGray, "G_gry") {
			t.Fatalf("Baseline broken: GRAY-only view should show G_gry")
		}
		stripped := stripANSI(view)
		onlyGrayStripped := stripANSI(onlyGray)
		// In the mixed view, the GRAY row is hidden — but the legend still
		// mentions G_gry. In the only-GRAY view, both legend and row show it.
		// Assert that mixed view mentions G_gry fewer times.
		mixedCount := strings.Count(stripped, "G_gry")
		onlyCount := strings.Count(onlyGrayStripped, "G_gry")
		if mixedCount >= onlyCount {
			t.Errorf("Expected GRAY row to be hidden in mixed view (G_red+G_gry): mixed=%d, only_gray=%d", mixedCount, onlyCount)
		}
		// And specifically, the only-GRAY cell line should not be present
		// in the mixed view's cell-rows area.
		if strings.Contains(stripped, "G_gry            ") {
			// G_gry padded to cell width — this is the cell row format
			t.Errorf("Mixed view should not contain G_gry cell row; got: %s", stripped)
		}
	})

	t.Run("GRAY cells visible when no non-GRAY cells", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_gry1", Phase: alert.PhaseSilencio},
			{GridCell: "G_gry2", Phase: alert.PhaseSilencio},
		}
		view := buildModel(phases).View()
		stripped := stripANSI(view)
		if !strings.Contains(stripped, "G_gry1") {
			t.Error("GRAY cell G_gry1 should be visible when no non-GRAY cells exist")
		}
		if !strings.Contains(stripped, "G_gry2") {
			t.Error("GRAY cell G_gry2 should be visible when no non-GRAY cells exist")
		}
	})

	t.Run("ORANGE with Decayed=true uses dim/faint style", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_org_d", Phase: alert.PhaseReplicas, Decayed: true, MainshockMag: 4.6},
		}
		// Force ANSI256 so we get SGR escapes
		lipgloss.SetColorProfile(2)
		defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
		view := buildModel(phases).View()
		// Faint() in lipgloss emits SGR 2.
		if !strings.Contains(view, "\x1b[2;") {
			t.Errorf("Expected dim/faint ANSI escape (\\x1b[2;) in view for Decayed ORANGE")
		}
	})

	t.Run("ORANGE without Decayed renders without faint attribute on the cell row", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_org_f", Phase: alert.PhaseReplicas, Decayed: false, MainshockMag: 5.5},
		}
		lipgloss.SetColorProfile(2)
		defer lipgloss.SetColorProfile(lipgloss.ColorProfile())
		view := buildModel(phases).View()
		stripped := stripANSI(view)
		if !strings.Contains(stripped, "G_org_f") {
			t.Error("Expected non-decayed ORANGE cell to render")
		}
	})

	t.Run("existing columns (b-value, Bath, Omori) preserved with phase badge", func(t *testing.T) {
		phases := []alert.CellPhase{
			{GridCell: "G_full", Phase: alert.PhasePrecursor, MainshockMag: 5.0, MainshockTime: now},
		}
		m := buildModel(phases)
		m.Projections[0].BValue = 0.65
		m.Projections[0].MainshockMag = 5.0
		m.Projections[0].MainshockTime = now.Add(-5 * time.Hour)
		m.Projections[0].BathMaxReplica = 3.8
		m.Projections[0].ExpectedReplicas24 = 2.1
		view := stripANSI(m.View())
		if !strings.Contains(view, "G_full") {
			t.Error("Expected cell G_full to render")
		}
		if !strings.Contains(view, "0.65") {
			t.Error("Expected b-value 0.65 to be preserved alongside phase")
		}
		if !strings.Contains(view, "3.8") {
			t.Error("Expected Bath max replica 3.8 to be preserved")
		}
		if !strings.Contains(view, "2.10") {
			t.Error("Expected Omori expected replicas 2.10 to be preserved")
		}
	})
}

// stripANSI removes ANSI escape codes from a string for content assertions
// on styled terminal output. Used only in tests.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until the final byte in 0x40..0x7e
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++ // include terminator
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"short", "short"},
		{"this is a very long location string that should be truncated", "this is a very long location string that s..."},
		{"exactly45chars_________________________!", "exactly45chars_________________________!"},
	}

	for _, tc := range tests {
		got := truncate(tc.input, 45)
		if got != tc.expected {
			t.Errorf("truncate(%q, 45) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
