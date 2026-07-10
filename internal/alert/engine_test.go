package alert

import (
	"testing"
	"time"
)

func TestClassifyDanger(t *testing.T) {
	tests := []struct {
		name      string
		magnitude float64
		distance  float64
		expected  AlertLevel
	}{
		{"Too far ignored", 8.0, 350.0, LevelInfo},
		{"Close Critical", 5.5, 30.0, LevelCritical},
		{"Close Pre-Alert", 4.0, 30.0, LevelPreAlert},
		{"Close Info", 3.0, 30.0, LevelInfo},
		{"Mid Critical", 6.2, 100.0, LevelCritical},
		{"Mid Pre-Alert", 4.8, 100.0, LevelPreAlert},
		{"Far Critical", 7.5, 250.0, LevelCritical},
		{"Far Pre-Alert", 5.8, 250.0, LevelPreAlert},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Sismo{Magnitude: tt.magnitude, Distance: tt.distance}
			result := ClassifyDanger(s)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s (Mag=%.1f, Dist=%.1f)", tt.expected, result, tt.magnitude, tt.distance)
			}
		})
	}
}

func TestSwarmQueue(t *testing.T) {
	q := NewSwarmQueue()
	now := time.Now()

	// 1. Add far event (should not be placed in the 300km swarm queue)
	sFar := Sismo{ID: "far", Distance: 350.0, Magnitude: 3.5, Time: now}
	if q.AddAndCheck(sFar) {
		t.Errorf("Swarm triggered by an event outside the 300km limit")
	}
	if len(q.GetEvents()) != 0 {
		t.Errorf("Far event incorrectly stored in SwarmQueue")
	}

	// 2. Add event with low magnitude (should not be placed in the swarm queue)
	sLowMag := Sismo{ID: "lowmag", Distance: 50.0, Magnitude: 2.5, Time: now}
	if q.AddAndCheck(sLowMag) {
		t.Errorf("Swarm triggered by low magnitude event")
	}
	if len(q.GetEvents()) != 0 {
		t.Errorf("Low magnitude event incorrectly stored in SwarmQueue")
	}

	// 3. Add first valid event
	s1 := Sismo{ID: "s1", Distance: 45.0, Magnitude: 3.0, Time: now}
	if q.AddAndCheck(s1) {
		t.Errorf("Swarm triggered with only 1 event")
	}

	// 4. Add second valid event
	s2 := Sismo{ID: "s2", Distance: 290.0, Magnitude: 3.2, Time: now.Add(5 * time.Minute)}
	if q.AddAndCheck(s2) {
		t.Errorf("Swarm triggered with only 2 events")
	}

	// 5. Add third valid event
	s3 := Sismo{ID: "s3", Distance: 10.0, Magnitude: 3.0, Time: now.Add(10 * time.Minute)}
	if q.AddAndCheck(s3) {
		t.Errorf("Swarm triggered with only 3 events")
	}

	// 6. Add fourth valid event
	s4 := Sismo{ID: "s4", Distance: 150.0, Magnitude: 4.0, Time: now.Add(15 * time.Minute)}
	if q.AddAndCheck(s4) {
		t.Errorf("Swarm triggered with only 4 events")
	}

	// 7. Add fifth valid event -> Swarm should trigger
	s5 := Sismo{ID: "s5", Distance: 100.0, Magnitude: 3.5, Time: now.Add(20 * time.Minute)}
	if !q.AddAndCheck(s5) {
		t.Errorf("Expected Swarm to trigger with 5 events under 300km (mag >= 3.0) inside 6h")
	}

	// 8. Verify old event pruning
	q2 := NewSwarmQueue()
	oldTime := now.Add(-7 * time.Hour)
	q2.events = append(q2.events, Sismo{ID: "old", Distance: 20.0, Magnitude: 3.5, Time: oldTime})

	sNew := Sismo{ID: "new", Distance: 50.0, Magnitude: 3.5, Time: now}
	q2.AddAndCheck(sNew)
	
	events := q2.GetEvents()
	if len(events) != 1 || events[0].ID != "new" {
		t.Errorf("Pruning failed: expected only 1 active event ('new'), got %d", len(events))
	}
}
