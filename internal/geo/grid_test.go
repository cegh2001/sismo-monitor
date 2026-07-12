package geo

import (
	"testing"
)

func TestGetGridCell(t *testing.T) {
	tests := []struct {
		name     string
		lat, lon float64
		expected string
	}{
		// Known Venezuelan points
		{
			name:     "La Guaira",
			lat:      10.60,
			lon:      -66.93,
			expected: "G_13_12",
		},
		{
			name:     "Caracas",
			lat:      10.48,
			lon:      -66.90,
			expected: "G_13_12",
		},
		{
			name:     "Maracaibo",
			lat:      10.63,
			lon:      -71.61,
			expected: "G_3_12",
		},
		{
			name:     "San Cristóbal",
			lat:      7.77,
			lon:      -72.23,
			expected: "G_1_6",
		},
		// Boundary edges
		{
			name:     "min corner (SW)",
			lat:      5.0,
			lon:      -73.0,
			expected: "G_0_0",
		},
		{
			name:     "max corner (NE)",
			lat:      13.0,
			lon:      -59.0,
			expected: "G_30_17",
		},
		// Out of bounds — lat too low
		{
			name:     "lat below min",
			lat:      4.9,
			lon:      -67.0,
			expected: "OUT_OF_BOUNDS",
		},
		// Out of bounds — lat too high
		{
			name:     "lat above max",
			lat:      13.1,
			lon:      -67.0,
			expected: "OUT_OF_BOUNDS",
		},
		// Out of bounds — lon too west
		{
			name:     "lon west of min",
			lat:      10.0,
			lon:      -73.1,
			expected: "OUT_OF_BOUNDS",
		},
		// Out of bounds — lon too east
		{
			name:     "lon east of max",
			lat:      10.0,
			lon:      -58.9,
			expected: "OUT_OF_BOUNDS",
		},
		// Far out of bounds
		{
			name:     "New York",
			lat:      40.71,
			lon:      -74.01,
			expected: "OUT_OF_BOUNDS",
		},
		{
			name:     "Brasilia",
			lat:      2.0,
			lon:      -60.0,
			expected: "OUT_OF_BOUNDS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetGridCell(tc.lat, tc.lon)
			if got != tc.expected {
				t.Errorf("GetGridCell(%.2f, %.2f) = %q, want %q", tc.lat, tc.lon, got, tc.expected)
			}
		})
	}
}

func TestGetGridCellConsistency(t *testing.T) {
	// Nearby points should map to the same grid cell
	cell1 := GetGridCell(10.60, -66.93) // La Guaira
	cell2 := GetGridCell(10.61, -66.92) // very close
	cell3 := GetGridCell(10.59, -66.94) // very close

	if cell1 != cell2 || cell2 != cell3 {
		t.Errorf("Nearby points should share same cell: %q, %q, %q", cell1, cell2, cell3)
	}

	// Distant points should map to different cells
	cellLaGuaira := GetGridCell(10.60, -66.93)
	cellMaracaibo := GetGridCell(10.63, -71.61)

	if cellLaGuaira == cellMaracaibo {
		t.Errorf("Distant points should differ: La Guaira=%q, Maracaibo=%q", cellLaGuaira, cellMaracaibo)
	}
}

func TestGetGridCellValidFormat(t *testing.T) {
	// All in-bounds cells must follow the "G_col_row" format
	cell := GetGridCell(10.0, -67.0)
	if len(cell) < 5 || cell[0] != 'G' || cell[1] != '_' {
		t.Errorf("Invalid cell format: %q", cell)
	}
}
