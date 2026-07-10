package geo

import (
	"math"
	"testing"
)

func TestHaversineDistance(t *testing.T) {
	// Test distance from La Guaira (10.60, -66.93) to a nearby point (10.50, -66.90)
	dist := DistanceToLaGuaira(10.50, -66.90)
	expected := 11.5

	if math.Abs(dist-expected) > 1.0 {
		t.Errorf("Expected distance to be roughly %.1f km, got %.1f km", expected, dist)
	}

	// Identical coordinates should yield exactly 0 km
	selfDist := DistanceToLaGuaira(LaGuairaLat, LaGuairaLon)
	if selfDist != 0.0 {
		t.Errorf("Expected distance of 0.0 to self, got %f", selfDist)
	}
}
