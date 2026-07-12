package ingest

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
	"sismo-monitor/internal/log"
)

// FDSNClient handles ingestion from FDSN Web Service event query endpoints.
type FDSNClient struct {
	sourceName   string
	baseURL      string
	pollInterval time.Duration
	client       *http.Client
	logger       func(string, ...interface{})
	errNotifier  func(error)
	seenEvents   map[string]time.Time
	mu           sync.Mutex
	statsCount   int
}

// NewFDSNClient creates a new FDSNClient.
func NewFDSNClient(sourceName string, baseURL string, pollInterval time.Duration, logger func(string, ...interface{}), errNotifier func(error)) *FDSNClient {
	return &FDSNClient{
		sourceName:   sourceName,
		baseURL:      baseURL,
		pollInterval: pollInterval,
		client:       &http.Client{Timeout: 15 * time.Second},
		logger:       logger,
		errNotifier:  errNotifier,
		seenEvents:   make(map[string]time.Time),
	}
}

// Start runs the polling loop with exponential backoff on fetch failures.
func (c *FDSNClient) Start(ctx context.Context, out chan<- alert.Sismo) {
	c.log("%s client starting. URL: %s, Interval: %v", c.sourceName, c.baseURL, c.pollInterval)

	backoff := c.pollInterval
	const maxBackoff = 5 * time.Minute

	for {
		select {
		case <-ctx.Done():
			c.log("%s client exiting.", c.sourceName)
			return
		default:
		}

		err := c.fetchAndDispatch(ctx, out)
		if err != nil {
			c.log("%s fetch failed: %v. Backing off %v...", c.sourceName, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
		} else {
			backoff = c.pollInterval // reset on success
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}
}

// GetStatsCount returns the request count.
func (c *FDSNClient) GetStatsCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statsCount
}

func (c *FDSNClient) incrementStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsCount++
}

func (c *FDSNClient) fetchAndDispatch(ctx context.Context, out chan<- alert.Sismo) error {
	c.incrementStats()
	events, err := c.Fetch(ctx)
	if err != nil {
		if c.errNotifier != nil {
			c.errNotifier(err)
		}
		return err
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

	c.log("%s client found %d Venezuelan events (%d new).", c.sourceName, len(events), len(newEvents))
	for _, e := range newEvents {
		select {
		case out <- e:
		default:
		}
	}
	return nil
}

// Fetch queries the FDSN service and parses the response.
func (c *FDSNClient) Fetch(ctx context.Context) ([]alert.Sismo, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	q := u.Query()
	q.Set("minlatitude", "5.0")
	q.Set("maxlatitude", "13.0")
	q.Set("minlongitude", "-73.0")
	q.Set("maxlongitude", "-59.0")
	
	// Query events from the last 24 hours
	startTime := time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02T15:04:05")
	q.Set("starttime", startTime)

	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP response code: %d", resp.StatusCode)
	}

	return c.ParseXML(resp.Body)
}

// ParseXML parses QuakeML data from an io.Reader and maps it to alert.Sismo.
func (c *FDSNClient) ParseXML(r io.Reader) ([]alert.Sismo, error) {
	qml, err := ParseQuakeML(r)
	if err != nil {
		return nil, fmt.Errorf("parsing QuakeML: %w", err)
	}

	var events []alert.Sismo
	for _, ev := range qml.EventParameters.Events {
		prefOrigin := ev.GetPreferredOrigin()
		prefMag := ev.GetPreferredMagnitude()

		if prefOrigin == nil {
			continue
		}

		lat := prefOrigin.Latitude.Value
		lon := prefOrigin.Longitude.Value
		depth := prefOrigin.Depth.Value / 1000.0 // meters to km

		gridCell := geo.GetGridCell(lat, lon)
		if gridCell == "OUT_OF_BOUNDS" {
			continue
		}

		var eventTime time.Time
		if prefOrigin.Time.Value != "" {
			var parseErr error
			eventTime, parseErr = time.Parse(time.RFC3339, prefOrigin.Time.Value)
			if parseErr != nil {
				eventTime, parseErr = time.Parse("2006-01-02T15:04:05", prefOrigin.Time.Value)
				if parseErr != nil {
					eventTime = time.Now()
				}
			}
		} else {
			eventTime = time.Now()
		}

		magnitude := 0.0
		if prefMag != nil {
			magnitude = prefMag.Mag.Value
		}

		location := ev.Description.Text
		if location == "" {
			location = fmt.Sprintf("Regional Event (%s)", c.sourceName)
		}

		cleanSource := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(c.sourceName, " ", ""), "(", ""))
		cleanSource = strings.ReplaceAll(cleanSource, ")", "")
		cleanSource = strings.ReplaceAll(cleanSource, "-", "")

		id := ev.PublicID
		if parts := strings.Split(id, "/"); len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		if parts := strings.Split(id, "="); len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		if parts := strings.Split(id, ":"); len(parts) > 0 {
			id = parts[len(parts)-1]
		}

		events = append(events, alert.Sismo{
			ID:        fmt.Sprintf("fdsn-%s-%s", cleanSource, id),
			Source:    c.sourceName,
			Magnitude: magnitude,
			Depth:     depth,
			Latitude:  lat,
			Longitude: lon,
			Location:  location,
			Time:      eventTime,
			Distance:  geo.DistanceToLaGuaira(lat, lon),
			GridCell:  gridCell,
		})
	}

	return events, nil
}

func (c *FDSNClient) log(format string, args ...interface{}) {
	log.Log(c.logger, format, args...)
}

// QuakeML and related structs for XML parsing.
type QuakeML struct {
	XMLName         xml.Name        `xml:"quakeml"`
	EventParameters EventParameters `xml:"eventParameters"`
}

type EventParameters struct {
	Events []Event `xml:"event"`
}

type Event struct {
	PublicID             string      `xml:"publicID,attr"`
	PreferredOriginID    string      `xml:"preferredOriginID"`
	PreferredMagnitudeID string      `xml:"preferredMagnitudeID"`
	Description          Description `xml:"description"`
	Origins              []Origin    `xml:"origin"`
	Magnitudes           []Magnitude `xml:"magnitude"`
}

type Description struct {
	Text string `xml:"text"`
	Type string `xml:"type"`
}

type Origin struct {
	PublicID  string   `xml:"publicID,attr"`
	Time      TimeVal  `xml:"time"`
	Latitude  FloatVal `xml:"latitude"`
	Longitude FloatVal `xml:"longitude"`
	Depth     FloatVal `xml:"depth"`
}

type Magnitude struct {
	PublicID string   `xml:"publicID,attr"`
	Mag      FloatVal `xml:"mag"`
	Type     string   `xml:"type"`
}

type FloatVal struct {
	Value float64 `xml:"value"`
}

type TimeVal struct {
	Value string `xml:"value"`
}

// GetPreferredOrigin returns the preferred origin, or the first available.
func (e *Event) GetPreferredOrigin() *Origin {
	if len(e.Origins) == 0 {
		return nil
	}
	if e.PreferredOriginID != "" {
		for i := range e.Origins {
			if e.Origins[i].PublicID == e.PreferredOriginID {
				return &e.Origins[i]
			}
		}
	}
	return &e.Origins[0]
}

// GetPreferredMagnitude returns the preferred magnitude, or the first available.
func (e *Event) GetPreferredMagnitude() *Magnitude {
	if len(e.Magnitudes) == 0 {
		return nil
	}
	if e.PreferredMagnitudeID != "" {
		for i := range e.Magnitudes {
			if e.Magnitudes[i].PublicID == e.PreferredMagnitudeID {
				return &e.Magnitudes[i]
			}
		}
	}
	return &e.Magnitudes[0]
}

// ParseQuakeML decodes QuakeML XML.
func ParseQuakeML(r io.Reader) (*QuakeML, error) {
	var qml QuakeML
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&qml); err != nil {
		return nil, err
	}
	return &qml, nil
}
