package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
	"sismo-monitor/internal/models"
)

// USGSClient polls the USGS real-time feed, parsing and dispatching Venezuelan events.
type USGSClient struct {
	url          string
	pollInterval time.Duration
	client       *http.Client
	logger       func(string, ...interface{})
	errNotifier  func(error)
	seenEvents   map[string]time.Time
	mu           sync.Mutex
	statsCount   int
}

// NewUSGSClient creates a new USGSClient.
func NewUSGSClient(url string, logger func(string, ...interface{}), errNotifier func(error)) *USGSClient {
	return &USGSClient{
		url:          url,
		pollInterval: 120 * time.Second,
		client:       &http.Client{Timeout: 15 * time.Second},
		logger:       logger,
		errNotifier:  errNotifier,
		seenEvents:   make(map[string]time.Time),
	}
}

// Start runs the polling loop.
func (c *USGSClient) Start(ctx context.Context, out chan<- alert.Sismo) {
	c.log("USGS client starting. URL: %s, Interval: %v", c.url, c.pollInterval)

	// Fetch immediately
	c.fetchAndDispatch(out)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.log("USGS client exiting.")
			return
		case <-ticker.C:
			c.fetchAndDispatch(out)
		}
	}
}

// GetStatsCount returns the request count.
func (c *USGSClient) GetStatsCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statsCount
}

func (c *USGSClient) incrementStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsCount++
}

func (c *USGSClient) fetchAndDispatch(out chan<- alert.Sismo) {
	c.incrementStats()
	events, err := c.Fetch()
	if err != nil {
		c.log("USGS fetch failed: %v", err)
		if c.errNotifier != nil {
			c.errNotifier(err)
		}
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-24 * time.Hour)
	for id, addedAt := range c.seenEvents {
		if addedAt.Before(cutoff) {
			delete(c.seenEvents, id)
		}
	}

	var newEvents []alert.Sismo
	for _, e := range events {
		if _, seen := c.seenEvents[e.ID]; !seen {
			c.seenEvents[e.ID] = time.Now()
			newEvents = append(newEvents, e)
		}
	}

	sort.Slice(newEvents, func(i, j int) bool {
		return newEvents[i].Time.Before(newEvents[j].Time)
	})

	c.log("USGS client found %d Venezuelan events (%d new).", len(events), len(newEvents))
	for _, e := range newEvents {
		select {
		case out <- e:
		default:
		}
	}
}

// Fetch requests the USGS GeoJSON and filters/maps it.
func (c *USGSClient) Fetch() ([]alert.Sismo, error) {
	resp, err := c.client.Get(c.url)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", c.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP response code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body failed: %w", err)
	}

	var geoJSON models.USGSFeatureCollection
	if err := json.Unmarshal(body, &geoJSON); err != nil {
		return nil, fmt.Errorf("unmarshal USGS GeoJSON failed: %w", err)
	}

	var events []alert.Sismo
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

		t := time.UnixMilli(f.Properties.Time)

		events = append(events, alert.Sismo{
			ID:        "usgs-" + f.ID,
			Source:    "USGS",
			Magnitude: f.Properties.Mag,
			Depth:     depth,
			Latitude:  lat,
			Longitude: lon,
			Location:  f.Properties.Place,
			Time:      t,
			Distance:  geo.DistanceToLaGuaira(lat, lon),
			GridCell:  gridCell,
		})
	}

	return events, nil
}

func (c *USGSClient) log(format string, args ...interface{}) {
	if c.logger != nil {
		c.logger(format, args...)
	}
}
