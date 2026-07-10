package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
	"sismo-monitor/internal/models"
)

// USGSHistoricalWorker fetches 2 years of historical seismic events to seed gap analysis.
type USGSHistoricalWorker struct {
	baseURL string
	client  *http.Client
	logger  func(string, ...interface{})
}

// NewUSGSHistoricalWorker creates a USGSHistoricalWorker.
func NewUSGSHistoricalWorker(baseURL string, logger func(string, ...interface{})) *USGSHistoricalWorker {
	return &USGSHistoricalWorker{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
		logger:  logger,
	}
}

// Fetch requests the USGS FDSN API for historical events within Venezuela region bounds.
func (w *USGSHistoricalWorker) Fetch(ctx context.Context, starttime time.Time) ([]alert.Sismo, error) {
	u, err := url.Parse(w.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid historical base URL: %w", err)
	}

	q := u.Query()
	q.Set("format", "geojson")
	q.Set("starttime", starttime.Format("2006-01-02"))
	q.Set("minmagnitude", "2.5")
	q.Set("minlatitude", fmt.Sprintf("%f", geo.MinLat))
	q.Set("maxlatitude", fmt.Sprintf("%f", geo.MaxLat))
	q.Set("minlongitude", fmt.Sprintf("%f", geo.MinLon))
	q.Set("maxlongitude", fmt.Sprintf("%f", geo.MaxLon))
	u.RawQuery = q.Encode()

	w.log("USGS Historical: Fetching from %s", u.String())

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("USGS historical HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("USGS historical HTTP response code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body failed: %w", err)
	}

	var geoJSON models.USGSFeatureCollection
	if err := json.Unmarshal(body, &geoJSON); err != nil {
		return nil, fmt.Errorf("unmarshal historical GeoJSON failed: %w", err)
	}

	var sismos []alert.Sismo
	for _, f := range geoJSON.Features {
		if len(f.Geometry.Coordinates) < 3 {
			continue
		}
		lon := f.Geometry.Coordinates[0]
		lat := f.Geometry.Coordinates[1]
		depth := f.Geometry.Coordinates[2]

		gridCell := geo.GetGridCell(lat, lon)
		if gridCell == "OUT_OF_BOUNDS" {
			continue
		}

		sismos = append(sismos, alert.Sismo{
			ID:        "usgs-" + f.ID,
			Source:    "USGS",
			Magnitude: f.Properties.Mag,
			Depth:     depth,
			Latitude:  lat,
			Longitude: lon,
			Location:  f.Properties.Place,
			Time:      time.UnixMilli(f.Properties.Time),
			Distance:  geo.DistanceToLaGuaira(lat, lon),
			GridCell:  gridCell,
		})
	}

	w.log("USGS Historical: Parsed %d events in region.", len(sismos))
	return sismos, nil
}

func (w *USGSHistoricalWorker) log(format string, args ...interface{}) {
	if w.logger != nil {
		w.logger(format, args...)
	}
}
