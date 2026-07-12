package alert

import (
	"testing"
	"time"
)

func TestSismoWaveArrivalTimes(t *testing.T) {
	originTime := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	s := Sismo{
		Magnitude: 5.5,
		Distance:  210.0, // km
		Time:      originTime,
	}

	// Expected travel times:
	// P-wave: 210.0 / 6.0 = 35 seconds
	// S-wave: 210.0 / 3.5 = 60 seconds
	expectedP := 35 * time.Second
	expectedS := 60 * time.Second

	if pTravel := s.PWaveTravelTime(); pTravel != expectedP {
		t.Errorf("Expected P-wave travel time %v, got %v", expectedP, pTravel)
	}

	if sTravel := s.SWaveTravelTime(); sTravel != expectedS {
		t.Errorf("Expected S-wave travel time %v, got %v", expectedS, sTravel)
	}

	expectedPArrival := originTime.Add(expectedP)
	expectedSArrival := originTime.Add(expectedS)

	if pArrival := s.PWaveArrivalTime(); pArrival != expectedPArrival {
		t.Errorf("Expected P-wave arrival time %v, got %v", expectedPArrival, pArrival)
	}

	if sArrival := s.SWaveArrivalTime(); sArrival != expectedSArrival {
		t.Errorf("Expected S-wave arrival time %v, got %v", expectedSArrival, sArrival)
	}
}

func TestSismoWaveArrivalTimesZeroDistance(t *testing.T) {
	s := Sismo{
		Magnitude: 4.0,
		Distance:  0.0,
		Time:      time.Now(),
	}

	if pTravel := s.PWaveTravelTime(); pTravel != 0 {
		t.Errorf("Expected P-wave travel time 0 for distance 0, got %v", pTravel)
	}

	if sTravel := s.SWaveTravelTime(); sTravel != 0 {
		t.Errorf("Expected S-wave travel time 0 for distance 0, got %v", sTravel)
	}
}
