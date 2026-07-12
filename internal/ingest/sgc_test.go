package ingest

import (
	"testing"
	"time"

	"sismo-monitor/internal/alert"
)

func TestParseExtractResult_Valid(t *testing.T) {
	s := &SGCScraper{}

	jsonStr := `[
		{"magnitude":"2.8M","place":"Mesetas - Meta, Colombia","dateTime":"2026-07-12 08:14","depth":"Superficial","latitude":"3.51","longitude":"-74.18","localizacionText":"Localización: 3.51°,-74.18°","sismoId":"SGC2026npltez", "nearby": "Municipios cercanos: Lejanías (Meta) a 17 km, Mesetas (Meta) a 20 km"},
		{"magnitude":"3.8M","place":"Mesetas - Meta, Colombia","dateTime":"2026-07-12 07:02","depth":"Superficial","latitude":"3.51","longitude":"-74.18","localizacionText":"Localización: 3.51°,-74.18°","sismoId":"SGC2026npjjsg", "nearby": ""},
		{"magnitude":"2.2M","place":"Venezuela","dateTime":"2026-07-12 00:40","depth":"Superficial","latitude":"9.02","longitude":"-70.57","localizacionText":"Localización: 9.02°,-70.57°","sismoId":"SGC2026nowsej", "nearby": "Municipios cercanos: Trujillo ( Venezuela) a 41 km, Barinas ( Venezuela) a 58 km"}
	]`

	events, err := s.parseExtractResult(jsonStr)
	if err != nil {
		t.Fatalf("parseExtractResult failed: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}

	// Verify Event 1 (Colombia - stays as original place)
	e1 := events[0]
	if e1.Magnitude != 2.8 {
		t.Errorf("Event 1: expected magnitude 2.8, got %.1f", e1.Magnitude)
	}
	if e1.Location != "Mesetas - Meta, Colombia" {
		t.Errorf("Event 1: expected place 'Mesetas - Meta, Colombia', got %q", e1.Location)
	}

	// Verify Event 3 (Venezuela - parsed to specific location)
	e3 := events[2]
	if e3.Magnitude != 2.2 {
		t.Errorf("Event 3: expected magnitude 2.2, got %.1f", e3.Magnitude)
	}
	if e3.Location != "Trujillo, Venezuela (41 km)" {
		t.Errorf("Event 3: expected place 'Trujillo, Venezuela (41 km)', got %q", e3.Location)
	}
}

func TestParseExtractResult_InvalidJSON(t *testing.T) {
	s := &SGCScraper{}

	jsonStr := `invalid json`
	_, err := s.parseExtractResult(jsonStr)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestParseSpecificLocation(t *testing.T) {
	t.Run("Venezuela format", func(t *testing.T) {
		res := parseSpecificLocation("Venezuela", "Municipios cercanos: Barinas ( Venezuela) a 44 km, Trujillo ( Venezuela) a 54 km")
		expected := "Barinas, Venezuela (44 km)"
		if res != expected {
			t.Errorf("Expected %q, got %q", expected, res)
		}
	})

	t.Run("empty nearby", func(t *testing.T) {
		res := parseSpecificLocation("Venezuela", "")
		expected := "Venezuela"
		if res != expected {
			t.Errorf("Expected %q, got %q", expected, res)
		}
	})
}

func TestValidateEvents(t *testing.T) {
	s := &SGCScraper{}

	t.Run("valid events", func(t *testing.T) {
		events := []alert.Sismo{
			{Magnitude: 4.5, Latitude: 10.7, Longitude: -62.5, Time: time.Now()},
			{Magnitude: 3.2, Latitude: 7.8, Longitude: -72.5, Time: time.Now()},
		}
		if err := s.validateEvents(events); err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	})

	t.Run("implausible magnitude", func(t *testing.T) {
		events := []alert.Sismo{
			{Magnitude: 15.0, Latitude: 10.7, Longitude: -62.5},
		}
		if err := s.validateEvents(events); err == nil {
			t.Error("Expected error for magnitude > 10")
		}
	})

	t.Run("implausible latitude", func(t *testing.T) {
		events := []alert.Sismo{
			{Magnitude: 4.5, Latitude: 95.0, Longitude: -62.5},
		}
		if err := s.validateEvents(events); err == nil {
			t.Error("Expected error for latitude > 90")
		}
	})

	t.Run("zero magnitude", func(t *testing.T) {
		events := []alert.Sismo{
			{Magnitude: 0.0, Latitude: 10.7, Longitude: -62.5},
		}
		if err := s.validateEvents(events); err == nil {
			t.Error("Expected error for magnitude <= 0")
		}
	})
}
