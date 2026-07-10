package ingest

import (
	"testing"
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
