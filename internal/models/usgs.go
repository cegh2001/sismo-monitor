package models

// USGSFeatureCollection represents a USGS GeoJSON feature collection.
type USGSFeatureCollection struct {
	Type     string        `json:"type"`
	Features []USGSFeature `json:"features"`
}

// USGSFeature represents a single seismic event in the GeoJSON feed.
type USGSFeature struct {
	Type       string         `json:"type"`
	Properties USGSProperties `json:"properties"`
	Geometry   USGSGeometry   `json:"geometry"`
	ID         string         `json:"id"`
}

// USGSProperties represents the properties of a USGS seismic event.
type USGSProperties struct {
	Mag     float64 `json:"mag"`
	Place   string  `json:"place"`
	Time    int64   `json:"time"` // Epoch milliseconds
	URL     string  `json:"url"`
	Detail  string  `json:"detail"`
	Status  string  `json:"status"`
	Tsunami int     `json:"tsunami"`
	Sig     int     `json:"sig"`
	Net     string  `json:"net"`
	Code    string  `json:"code"`
	Ids     string  `json:"ids"`
	Sources string  `json:"sources"`
	Types   string  `json:"types"`
	Title   string  `json:"title"`
}

// USGSGeometry represents the spatial geometry of a USGS seismic event.
type USGSGeometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"` // [longitude, latitude, depth]
}
