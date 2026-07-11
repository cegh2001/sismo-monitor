package alert

import (
	"math"
	"testing"
	"time"
)

func TestCalculateBValue(t *testing.T) {
	// Case 1: Normal calculation
	// Magnitudes: 2.5, 3.5, 4.5, 5.5. Mc = 2.5, deltaM = 0.1
	// Count = 4, Sum = 16.0, Avg = 4.0
	// Denominator = 4.0 - (2.5 - 0.05) = 4.0 - 2.45 = 1.55
	// Expected b = log10(e) / 1.55 = 0.4342944819032518 / 1.55 = 0.280190
	mags := []float64{2.5, 3.5, 4.5, 5.5}
	b := CalculateBValue(mags, 2.5, 0.1)
	expected := math.Log10(math.E) / 1.55
	if math.Abs(b-expected) > 1e-6 {
		t.Errorf("Expected b-value %.6f, got %.6f", expected, b)
	}

	// Case 2: Events below Mc should be filtered out
	// Magnitudes: 1.0, 2.0, 3.0, 4.0. Mc = 2.5, deltaM = 0.1
	// Filtered magnitudes: 3.0, 4.0. Count = 2, Sum = 7.0, Avg = 3.5
	// Denominator = 3.5 - 2.45 = 1.05
	// Expected b = log10(e) / 1.05 = 0.413614
	mags2 := []float64{1.0, 2.0, 3.0, 4.0}
	b2 := CalculateBValue(mags2, 2.5, 0.1)
	expected2 := math.Log10(math.E) / 1.05
	if math.Abs(b2-expected2) > 1e-6 {
		t.Errorf("Expected b-value %.6f, got %.6f", expected2, b2)
	}

	// Case 3: Empty inputs
	b3 := CalculateBValue([]float64{}, 2.5, 0.1)
	if b3 != 0.0 {
		t.Errorf("Expected b-value 0.0 for empty input, got %f", b3)
	}

	// Case 4: Zero events above Mc
	b4 := CalculateBValue([]float64{1.5, 2.4}, 2.5, 0.1)
	if b4 != 0.0 {
		t.Errorf("Expected b-value 0.0 for all below Mc, got %f", b4)
	}
}

func TestBathLaw(t *testing.T) {
	// Test normal calculation in AnalyzeGridCell
	now := time.Now()
	sismos := []Sismo{
		{
			ID:        "mainshock",
			GridCell:  "G_1_1",
			Magnitude: 6.2,
			Latitude:  10.0,
			Longitude: -67.0,
			Time:      now.Add(-2 * time.Hour),
		},
	}

	proj := AnalyzeGridCell("G_1_1", sismos, now)
	expectedBath := 6.2 - 1.2
	if proj.BathMaxReplica != expectedBath {
		t.Errorf("Expected Bath max replica %f, got %f", expectedBath, proj.BathMaxReplica)
	}

	// Test clamping below 0
	sismosLow := []Sismo{
		{
			ID:        "mainshock-low",
			GridCell:  "G_1_1",
			Magnitude: 1.0,
			Latitude:  10.0,
			Longitude: -67.0,
			Time:      now.Add(-2 * time.Hour),
		},
	}
	projLow := AnalyzeGridCell("G_1_1", sismosLow, now)
	if projLow.BathMaxReplica != 0.0 {
		t.Errorf("Expected clamped Bath max replica 0.0, got %f", projLow.BathMaxReplica)
	}
}

func TestOmoriReplicaRates(t *testing.T) {
	now := time.Now()
	mainshockTime := now.Add(-5 * time.Hour) // 5 hours ago

	sismos := []Sismo{
		// Mainshock
		{
			ID:        "mainshock",
			GridCell:  "G_1_1",
			Magnitude: 5.5,
			Latitude:  10.0,
			Longitude: -67.0,
			Time:      mainshockTime,
		},
		// 5 replicas after mainshock
		{ID: "rep1", GridCell: "G_1_1", Magnitude: 3.0, Latitude: 10.0, Longitude: -67.0, Time: mainshockTime.Add(1 * time.Hour)},
		{ID: "rep2", GridCell: "G_1_1", Magnitude: 3.2, Latitude: 10.0, Longitude: -67.0, Time: mainshockTime.Add(2 * time.Hour)},
		{ID: "rep3", GridCell: "G_1_1", Magnitude: 2.8, Latitude: 10.0, Longitude: -67.0, Time: mainshockTime.Add(3 * time.Hour)},
		{ID: "rep4", GridCell: "G_1_1", Magnitude: 3.5, Latitude: 10.0, Longitude: -67.0, Time: mainshockTime.Add(4 * time.Hour)},
		{ID: "rep5", GridCell: "G_1_1", Magnitude: 3.1, Latitude: 10.0, Longitude: -67.0, Time: now},
	}

	proj := AnalyzeGridCell("G_1_1", sismos, now)

	if proj.OmoriReplicaRate <= 0.0 {
		t.Errorf("Expected positive Omori replica rate, got %f", proj.OmoriReplicaRate)
	}

	tElapsed := 5.0
	cVal := 0.5
	expectedK := 5.0 / math.Log((tElapsed+cVal)/cVal)
	expectedRate := expectedK / (tElapsed + cVal)
	expectedReplicas := expectedK * math.Log((tElapsed+24.0+cVal)/(tElapsed+cVal))

	if math.Abs(proj.OmoriReplicaRate-expectedRate) > 1e-5 {
		t.Errorf("Expected OmoriReplicaRate %f, got %f", expectedRate, proj.OmoriReplicaRate)
	}

	if math.Abs(proj.ExpectedReplicas24-expectedReplicas) > 1e-5 {
		t.Errorf("Expected ExpectedReplicas24 %f, got %f", expectedReplicas, proj.ExpectedReplicas24)
	}
}

func TestGetFaultName(t *testing.T) {
	tests := []struct {
		lat, lon float64
		expected string
	}{
		{8.0, -71.5, "Falla de Boconó"},
		{10.5, -67.2, "Falla de San Sebastián"},
		{10.6, -63.0, "Falla de El Pilar"},
		{2.0, -60.0, "Falla Desconocida"},
	}

	for _, tc := range tests {
		name := GetFaultName(tc.lat, tc.lon)
		if name != tc.expected {
			t.Errorf("For coordinates (%.1f, %.1f) expected %q, got %q", tc.lat, tc.lon, tc.expected, name)
		}
	}
}

func TestComputeProjections(t *testing.T) {
	now := time.Now()
	sismos := []Sismo{
		{
			ID:        "s1",
			GridCell:  "G_2_2",
			Magnitude: 3.0,
			Latitude:  10.5,
			Longitude: -66.0,
			Time:      now.Add(-1 * time.Hour),
		},
		{
			ID:        "s2",
			GridCell:  "G_1_1",
			Magnitude: 5.0,
			Latitude:  8.0,
			Longitude: -71.5,
			Time:      now.Add(-2 * time.Hour),
		},
		{
			ID:        "s3",
			GridCell:  "OUT_OF_BOUNDS",
			Magnitude: 4.0,
			Latitude:  15.0,
			Longitude: -60.0,
			Time:      now.Add(-3 * time.Hour),
		},
		{
			ID:        "s4",
			GridCell:  "",
			Magnitude: 2.0,
			Latitude:  0.0,
			Longitude: 0.0,
			Time:      now.Add(-4 * time.Hour),
		},
		{
			ID:        "s5",
			GridCell:  "G_1_1",
			Magnitude: 3.5,
			Latitude:  8.0,
			Longitude: -71.5,
			Time:      now.Add(-10 * time.Minute),
		},
	}

	projections := ComputeProjections(sismos, now)

	if len(projections) != 2 {
		t.Fatalf("Expected 2 projections, got %d", len(projections))
	}

	if projections[0].GridCell != "G_1_1" {
		t.Errorf("Expected first projection cell to be 'G_1_1', got %q", projections[0].GridCell)
	}
	if projections[0].EventCount != 2 {
		t.Errorf("Expected event count for G_1_1 to be 2, got %d", projections[0].EventCount)
	}

	if projections[1].GridCell != "G_2_2" {
		t.Errorf("Expected second projection cell to be 'G_2_2', got %q", projections[1].GridCell)
	}
	if projections[1].EventCount != 1 {
		t.Errorf("Expected event count for G_2_2 to be 1, got %d", projections[1].EventCount)
	}
}
