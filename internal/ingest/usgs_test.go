package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"sismo-monitor/internal/alert"
)

func TestUSGSClientParsingAndFiltering(t *testing.T) {
	// Mock GeoJSON response.
	// Contains:
	// 1. One event inside Venezuela (Lat: 10.5, Lon: -66.9)
	// 2. One event outside Venezuela (Lat: 40.7128, Lon: -74.0060)
	mockGeoJSON := `{
		"type": "FeatureCollection",
		"features": [
			{
				"type": "Feature",
				"properties": {
					"mag": 3.2,
					"place": "10km N of Caracas, Venezuela",
					"time": 1720645000000,
					"url": "https://earthquake.usgs.gov/event1",
					"detail": "https://earthquake.usgs.gov/detail1",
					"title": "M 3.2 - 10km N of Caracas"
				},
				"geometry": {
					"type": "Point",
					"coordinates": [-66.9, 10.5, 12.4]
				},
				"id": "us1000abc"
			},
			{
				"type": "Feature",
				"properties": {
					"mag": 4.5,
					"place": "New York, USA",
					"time": 1720646000000,
					"url": "https://earthquake.usgs.gov/event2",
					"detail": "https://earthquake.usgs.gov/detail2",
					"title": "M 4.5 - New York"
				},
				"geometry": {
					"type": "Point",
					"coordinates": [-74.0060, 40.7128, 5.0]
				},
				"id": "us1000def"
			}
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockGeoJSON))
	}))
	defer ts.Close()

	client := NewUSGSClient(ts.URL, nil, nil)
	events, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	// The New York event should be filtered out because it is OUT_OF_BOUNDS.
	if len(events) != 1 {
		t.Fatalf("Expected 1 event (Venezuelan only), got %d", len(events))
	}

	e := events[0]
	if e.ID != "usgs-us1000abc" {
		t.Errorf("Expected ID 'usgs-us1000abc', got %q", e.ID)
	}
	if e.Magnitude != 3.2 {
		t.Errorf("Expected Magnitude 3.2, got %.1f", e.Magnitude)
	}
	if e.Latitude != 10.5 || e.Longitude != -66.9 {
		t.Errorf("Expected Lat/Lon (10.5, -66.9), got (%.2f, %.2f)", e.Latitude, e.Longitude)
	}
	if e.Depth != 12.4 {
		t.Errorf("Expected Depth 12.4, got %.1f", e.Depth)
	}
	if e.Location != "10km N of Caracas, Venezuela" {
		t.Errorf("Expected Place '10km N of Caracas, Venezuela', got %q", e.Location)
	}
	if e.GridCell == "" || e.GridCell == "OUT_OF_BOUNDS" {
		t.Errorf("Expected valid GridCell, got %q", e.GridCell)
	}
}

func TestUSGSClientDeduplication(t *testing.T) {
	mockGeoJSON := `{
		"type": "FeatureCollection",
		"features": [
			{
				"type": "Feature",
				"properties": {
					"mag": 2.8,
					"place": "Maracaibo, Venezuela",
					"time": 1720645000000,
					"url": "https://earthquake.usgs.gov/event1",
					"detail": "https://earthquake.usgs.gov/detail1",
					"title": "M 2.8 - Maracaibo"
				},
				"geometry": {
					"type": "Point",
					"coordinates": [-71.6, 10.6, 10.0]
				},
				"id": "us123"
			}
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockGeoJSON))
	}))
	defer ts.Close()

	client := NewUSGSClient(ts.URL, nil, nil)
	out := make(chan alert.Sismo, 10)

	// First fetch should dispatch the event
	_ = client.fetchAndDispatch(context.Background(), out)
	if len(out) != 1 {
		t.Fatalf("Expected 1 event dispatched, got %d", len(out))
	}
	_ = <-out

	// Second fetch should deduplicate it
	_ = client.fetchAndDispatch(context.Background(), out)
	if len(out) != 0 {
		t.Fatalf("Expected 0 events dispatched (deduplicated), got %d", len(out))
	}
}
