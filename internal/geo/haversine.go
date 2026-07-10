package geo

import (
	"math"
)

const (
	// EarthRadiusKm represents the radius of the Earth in kilometers.
	EarthRadiusKm = 6371.0

	// LaGuairaLat and LaGuairaLon define the coordinates of La Guaira, Venezuela.
	LaGuairaLat = 10.60
	LaGuairaLon = -66.93
)

// HaversineDistance calculates the great-circle distance between two points in kilometers.
func HaversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0

	rLat1 := lat1 * math.Pi / 180.0
	rLat2 := lat2 * math.Pi / 180.0

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Sin(dLon/2)*math.Sin(dLon/2)*math.Cos(rLat1)*math.Cos(rLat2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return EarthRadiusKm * c
}

// DistanceToLaGuaira calculates the distance from a given point to La Guaira in kilometers.
func DistanceToLaGuaira(lat, lon float64) float64 {
	return HaversineDistance(LaGuairaLat, LaGuairaLon, lat, lon)
}
