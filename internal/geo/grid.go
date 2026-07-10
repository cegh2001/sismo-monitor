package geo

import (
	"fmt"
	"math"
)

// Bounding box for the Venezuelan region.
const (
	MinLat     = 5.0
	MaxLat     = 13.0
	MinLon     = -73.0
	MaxLon     = -59.0
	GridSizeKm = 50.0
)

// GetGridCell calculates a grid cell ID for a given latitude and longitude.
// If the coordinates are outside the Venezuelan bounding box, it returns "OUT_OF_BOUNDS".
func GetGridCell(lat, lon float64) string {
	if lat < MinLat || lat > MaxLat || lon < MinLon || lon > MaxLon {
		return "OUT_OF_BOUNDS"
	}

	// Calculate distance in km from MinLat (y-axis)
	yKm := (lat - MinLat) * 111.12

	// Calculate distance in km from MinLon (x-axis)
	// We use the latitude of the point to account for earth's curvature.
	radLat := lat * math.Pi / 180.0
	xKm := (lon - MinLon) * 111.12 * math.Cos(radLat)

	col := int(math.Floor(xKm / GridSizeKm))
	row := int(math.Floor(yKm / GridSizeKm))

	return fmt.Sprintf("G_%d_%d", col, row)
}
