package alert

import (
	"testing"
	"time"
)

func TestDeduplicatorNoOverlap(t *testing.T) {
	d := NewDeduplicator(120*time.Second, 50.0)

	ev1 := Sismo{
		ID:        "emsc-1",
		Source:    "EMSC",
		Magnitude: 5.0,
		Depth:     10.0,
		Latitude:  10.0,
		Longitude: -66.0,
		Time:      time.Now(),
	}

	ev2 := Sismo{
		ID:        "fun-1",
		Source:    "Funvisis",
		Magnitude: 4.5,
		Depth:     5.0,
		Latitude:  11.0, // 1 degree difference is ~111km, so > 50km
		Longitude: -66.0,
		Time:      ev1.Time.Add(10 * time.Second),
	}

	res1, isUpdate1 := d.Add(ev1)
	if isUpdate1 {
		t.Errorf("Expected ev1 to not be an update")
	}
	if res1.ID != ev1.ID {
		t.Errorf("Expected result ID to match ev1 ID")
	}

	res2, isUpdate2 := d.Add(ev2)
	if isUpdate2 {
		t.Errorf("Expected ev2 to not be an update")
	}
	if res2.ID != ev2.ID {
		t.Errorf("Expected result ID to match ev2 ID")
	}
}

func TestDeduplicatorEMSCThenFunvisis(t *testing.T) {
	d := NewDeduplicator(120*time.Second, 50.0)

	baseTime := time.Now()

	// EMSC arrives first: better magnitude (5.5) but less accurate location
	evEMSC := Sismo{
		ID:        "emsc-1",
		Source:    "EMSC",
		Magnitude: 5.5,
		Depth:     20.0,
		Latitude:  10.65,
		Longitude: -66.90,
		Location:  "Near Caracas",
		Distance:  12.0,
		GridCell:  "G_1_1",
		Time:      baseTime,
	}

	// Funvisis arrives second: less accurate magnitude (5.1) but more accurate location
	evFun := Sismo{
		ID:        "fun-1",
		Source:    "Funvisis",
		Magnitude: 5.1,
		Depth:     12.5,
		Latitude:  10.60,
		Longitude: -66.93,
		Location:  "La Guaira Port",
		Distance:  0.0,
		GridCell:  "G_0_0",
		Time:      baseTime.Add(15 * time.Second),
	}

	res1, isUpdate1 := d.Add(evEMSC)
	if isUpdate1 {
		t.Fatalf("First event should not trigger update")
	}
	if res1.ID != "emsc-1" {
		t.Errorf("Expected ID 'emsc-1', got %q", res1.ID)
	}

	res2, isUpdate2 := d.Add(evFun)
	if !isUpdate2 {
		t.Fatalf("Second event should trigger update")
	}

	// Verify Fusion:
	// 1. Keeps the original ID
	if res2.ID != "emsc-1" {
		t.Errorf("Expected fused ID 'emsc-1', got %q", res2.ID)
	}
	// 2. Prioritizes Funvisis coordinates and depth
	if res2.Latitude != 10.60 || res2.Longitude != -66.93 {
		t.Errorf("Expected fused Lat/Lon (10.60, -66.93), got (%.2f, %.2f)", res2.Latitude, res2.Longitude)
	}
	if res2.Depth != 12.5 {
		t.Errorf("Expected fused depth 12.5, got %.1f", res2.Depth)
	}
	if res2.Location != "La Guaira Port" {
		t.Errorf("Expected fused location 'La Guaira Port', got %q", res2.Location)
	}
	// 3. Prioritizes EMSC magnitude
	if res2.Magnitude != 5.5 {
		t.Errorf("Expected fused magnitude 5.5, got %.1f", res2.Magnitude)
	}
	// 4. Earliest time
	if !res2.Time.Equal(baseTime) {
		t.Errorf("Expected fused time to be earliest time (%v), got %v", baseTime, res2.Time)
	}
	// 5. Combined source
	if res2.Source != "EMSC+Funvisis" {
		t.Errorf("Expected combined source 'EMSC+Funvisis', got %q", res2.Source)
	}
}

func TestDeduplicatorFunvisisThenEMSC(t *testing.T) {
	d := NewDeduplicator(120*time.Second, 50.0)

	baseTime := time.Now()

	// Funvisis arrives first
	evFun := Sismo{
		ID:        "fun-1",
		Source:    "Funvisis",
		Magnitude: 5.1,
		Depth:     12.5,
		Latitude:  10.60,
		Longitude: -66.93,
		Location:  "La Guaira Port",
		Distance:  0.0,
		GridCell:  "G_0_0",
		Time:      baseTime,
	}

	// EMSC arrives second
	evEMSC := Sismo{
		ID:        "emsc-1",
		Source:    "EMSC",
		Magnitude: 5.5,
		Depth:     20.0,
		Latitude:  10.65,
		Longitude: -66.90,
		Location:  "Near Caracas",
		Distance:  12.0,
		GridCell:  "G_1_1",
		Time:      baseTime.Add(10 * time.Second),
	}

	_, isUpdate1 := d.Add(evFun)
	if isUpdate1 {
		t.Fatalf("First event should not trigger update")
	}

	res2, isUpdate2 := d.Add(evEMSC)
	if !isUpdate2 {
		t.Fatalf("Second event should trigger update")
	}

	// Verify Fusion:
	// 1. Keeps the original ID (fun-1)
	if res2.ID != "fun-1" {
		t.Errorf("Expected fused ID 'fun-1', got %q", res2.ID)
	}
	// 2. Prioritizes Funvisis coordinates and depth
	if res2.Latitude != 10.60 || res2.Longitude != -66.93 {
		t.Errorf("Expected fused Lat/Lon (10.60, -66.93), got (%.2f, %.2f)", res2.Latitude, res2.Longitude)
	}
	// 3. Prioritizes EMSC magnitude
	if res2.Magnitude != 5.5 {
		t.Errorf("Expected fused magnitude 5.5, got %.1f", res2.Magnitude)
	}
	// 4. Combined source
	if res2.Source != "Funvisis+EMSC" {
		t.Errorf("Expected combined source 'Funvisis+EMSC', got %q", res2.Source)
	}
}
