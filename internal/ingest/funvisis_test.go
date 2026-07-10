package ingest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sismo-monitor/internal/alert"
)

func TestFunvisisHTMLParser(t *testing.T) {
	scraper := NewFunvisisScraper(nil, nil)

	// Mock HTML representing the typical Funvisis structure
	mockHTML := `
	<html>
		<body>
			<table class="recent-sismos">
				<thead>
					<tr>
						<th>Fecha (HLV)</th>
						<th>Hora (HLV)</th>
						<th>Latitud</th>
						<th>Longitud</th>
						<th>Profundidad (km)</th>
						<th>Magnitud (Mw)</th>
						<th>Localizacion</th>
					</tr>
				</thead>
				<tbody>
					<tr>
						<td>10-07-2026</td>
						<td>16:20:45</td>
						<td>10.65</td>
						<td>-66.90</td>
						<td>12.5</td>
						<td>3.5</td>
						<td>10 km al norte de La Guaira</td>
					</tr>
					<tr>
						<td>10-07-2026</td>
						<td>15:10:00</td>
						<td>10.50</td>
						<td>-66.95</td>
						<td>5.0</td>
						<td>2.8</td>
						<td>Caracas Valley</td>
					</tr>
				</tbody>
			</table>
		</body>
	</html>
	`

	events, err := scraper.ParseHTML(mockHTML)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}

	e := events[0]
	if e.Magnitude != 3.5 {
		t.Errorf("Expected Magnitude 3.5, got %.1f", e.Magnitude)
	}
	if e.Latitude != 10.65 || e.Longitude != -66.90 {
		t.Errorf("Expected Lat/Lon (10.65, -66.90), got (%.2f, %.2f)", e.Latitude, e.Longitude)
	}
	if e.Depth != 12.5 {
		t.Errorf("Expected Depth 12.5, got %.1f", e.Depth)
	}
	if e.Location != "10 km al norte de La Guaira" {
		t.Errorf("Expected Location '10 km al norte de La Guaira', got %q", e.Location)
	}
	if e.Source != "Funvisis" {
		t.Errorf("Expected Source 'Funvisis', got %q", e.Source)
	}

	// Test fallback parser
	_, err = scraper.ParseHTMLFallback("")
	if err == nil {
		t.Fatalf("Expected ParseHTMLFallback to return an error, but got nil")
	}
}

func TestFunvisisJSONParser(t *testing.T) {
	scraper := NewFunvisisScraper(nil, nil)

	mockJSON := `
	{
		"type": "FeatureCollection",
		"features": [
			{
				"type": "Feature",
				"geometry": {
					"type": "Point",
					"coordinates": [-66.90, 10.65]
				},
				"properties": {
					"phoneFormatted": "12.5 km",
					"phone": "3.5",
					"address": "10 km al norte de La Guaira",
					"city": "16:20:45",
					"country": "Venezuela",
					"postalCode": "10-07-2026",
					"state": "12.5 km",
					"lat": "10.65",
					"long": "-66.90"
				}
			}
		]
	}
	`

	events, err := scraper.ParseJSON(mockJSON)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Magnitude != 3.5 {
		t.Errorf("Expected Magnitude 3.5, got %.1f", e.Magnitude)
	}
	if e.Latitude != 10.65 || e.Longitude != -66.90 {
		t.Errorf("Expected Lat/Lon (10.65, -66.90), got (%.2f, %.2f)", e.Latitude, e.Longitude)
	}
	if e.Depth != 12.5 {
		t.Errorf("Expected Depth 12.5, got %.1f", e.Depth)
	}
	if e.Location != "10 km al norte de La Guaira" {
		t.Errorf("Expected Location '10 km al norte de La Guaira', got %q", e.Location)
	}
}

func TestFunvisisDeduplication(t *testing.T) {
	mockJSON := `
	{
		"type": "FeatureCollection",
		"features": [
			{
				"type": "Feature",
				"geometry": {
					"type": "Point",
					"coordinates": [-66.90, 10.65]
				},
				"properties": {
					"phoneFormatted": "12.5 km",
					"phone": "3.5",
					"address": "10 km al norte de La Guaira",
					"city": "16:20:45",
					"country": "Venezuela",
					"postalCode": "10-07-2026",
					"state": "12.5 km",
					"lat": "10.65",
					"long": "-66.90"
				}
			}
		]
	}
	`

	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockJSON))
	}))
	defer server.Close()

	scraper := NewFunvisisScraper(nil, nil)
	scraper.url = server.URL

	out := make(chan alert.Sismo, 10)

	// First scrape should dispatch the event
	scraper.scrapeAndDispatch(out)
	if len(out) != 1 {
		t.Fatalf("Expected 1 event on first scrape, got %d", len(out))
	}
	ev1 := <-out

	// Second scrape should NOT dispatch any event (since it's a duplicate)
	scraper.scrapeAndDispatch(out)
	if len(out) != 0 {
		t.Fatalf("Expected 0 events on second scrape (deduplicated), got %d", len(out))
	}

	// Now modify the server response to have a new event
	mockJSON2 := `
	{
		"type": "FeatureCollection",
		"features": [
			{
				"type": "Feature",
				"geometry": {
					"type": "Point",
					"coordinates": [-66.90, 10.65]
				},
				"properties": {
					"phoneFormatted": "12.5 km",
					"phone": "3.5",
					"address": "10 km al norte de La Guaira",
					"city": "16:20:45",
					"country": "Venezuela",
					"postalCode": "10-07-2026",
					"state": "12.5 km",
					"lat": "10.65",
					"long": "-66.90"
				}
			},
			{
				"type": "Feature",
				"geometry": {
					"type": "Point",
					"coordinates": [-66.95, 10.50]
				},
				"properties": {
					"phoneFormatted": "5.0 km",
					"phone": "2.8",
					"address": "Caracas Valley",
					"city": "15:10:00",
					"country": "Venezuela",
					"postalCode": "10-07-2026",
					"state": "5.0 km",
					"lat": "10.50",
					"long": "-66.95"
				}
			}
		]
	}
	`

	// Update server response
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockJSON2))
	})

	// Third scrape should only dispatch the new event
	scraper.scrapeAndDispatch(out)
	if len(out) != 1 {
		t.Fatalf("Expected 1 new event on third scrape, got %d", len(out))
	}
	ev2 := <-out
	if ev2.ID == ev1.ID {
		t.Errorf("Expected different event ID for the new event, but got same: %s", ev2.ID)
	}
}

func TestFunvisisSorting(t *testing.T) {
	// mockJSON contains 3 events: newest first (18:00:00), then middle (15:00:00), then oldest (12:00:00)
	mockJSON := `
	{
		"type": "FeatureCollection",
		"features": [
			{
				"type": "Feature",
				"geometry": {"type": "Point", "coordinates": [-66.90, 10.65]},
				"properties": {
					"phone": "3.0",
					"address": "Newest Event",
					"city": "18:00:00",
					"postalCode": "10-07-2026",
					"lat": "10.65",
					"long": "-66.90"
				}
			},
			{
				"type": "Feature",
				"geometry": {"type": "Point", "coordinates": [-66.90, 10.65]},
				"properties": {
					"phone": "2.0",
					"address": "Middle Event",
					"city": "15:00:00",
					"postalCode": "10-07-2026",
					"lat": "10.65",
					"long": "-66.90"
				}
			},
			{
				"type": "Feature",
				"geometry": {"type": "Point", "coordinates": [-66.90, 10.65]},
				"properties": {
					"phone": "1.0",
					"address": "Oldest Event",
					"city": "12:00:00",
					"postalCode": "10-07-2026",
					"lat": "10.65",
					"long": "-66.90"
				}
			}
		]
	}
	`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockJSON))
	}))
	defer server.Close()

	scraper := NewFunvisisScraper(nil, nil)
	scraper.url = server.URL

	out := make(chan alert.Sismo, 10)

	scraper.scrapeAndDispatch(out)
	if len(out) != 3 {
		t.Fatalf("Expected 3 events dispatched, got %d", len(out))
	}

	ev1 := <-out // Should be oldest (12:00:00)
	ev2 := <-out // Should be middle (15:00:00)
	ev3 := <-out // Should be newest (18:00:00)

	if ev1.Location != "Oldest Event" {
		t.Errorf("Expected first dispatched event to be 'Oldest Event', got %q", ev1.Location)
	}
	if ev2.Location != "Middle Event" {
		t.Errorf("Expected second dispatched event to be 'Middle Event', got %q", ev2.Location)
	}
	if ev3.Location != "Newest Event" {
		t.Errorf("Expected third dispatched event to be 'Newest Event', got %q", ev3.Location)
	}
}
