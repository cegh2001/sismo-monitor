package ingest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// FunvisisScraper polls the Funvisis website, scraping and parsing recent seismic events.
type FunvisisScraper struct {
	url          string
	pollInterval time.Duration
	client       *http.Client
	logger       func(string, ...interface{})
	errNotifier  func(error)
}

// NewFunvisisScraper creates a new FunvisisScraper.
func NewFunvisisScraper(logger func(string, ...interface{}), errNotifier func(error)) *FunvisisScraper {
	return &FunvisisScraper{
		url:          "http://www.funvisis.gob.ve/maravilla.json",
		pollInterval: 120 * time.Second, // Poll every 2 minutes
		client:       &http.Client{Timeout: 15 * time.Second},
		logger:       logger,
		errNotifier:  errNotifier,
	}
}

// Start starts the polling loop, scraping Funvisis at regular intervals.
func (s *FunvisisScraper) Start(ctx context.Context, out chan<- alert.Sismo) {
	s.log("Funvisis scraper starting. Interval: %v", s.pollInterval)

	// First scrape immediately
	s.scrapeAndDispatch(out)

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log("Funvisis scraper exiting.")
			return
		case <-ticker.C:
			s.scrapeAndDispatch(out)
		}
	}
}

func (s *FunvisisScraper) scrapeAndDispatch(out chan<- alert.Sismo) {
	events, err := s.Scrape()
	if err != nil {
		s.log("Funvisis scrape failed: %v", err)
		if s.errNotifier != nil {
			s.errNotifier(err)
		}
		return
	}

	s.log("Funvisis scraper found %d events.", len(events))
	for _, e := range events {
		select {
		case out <- e:
		default:
		}
	}
}

// Scrape performs the HTTP request and parses the HTML or JSON body.
func (s *FunvisisScraper) Scrape() ([]alert.Sismo, error) {
	resp, err := s.client.Get(s.url)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", s.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP response code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body failed: %w", err)
	}

	content := string(body)
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return s.ParseJSON(trimmed)
	}

	events, err := s.ParseHTML(content)
	if err != nil {
		return nil, fmt.Errorf("HTML parsing failed: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no events parsed from HTML table")
	}

	return events, nil
}

// ParseHTML uses regex to extract table rows and cells from HTML.
func (s *FunvisisScraper) ParseHTML(html string) ([]alert.Sismo, error) {
	var events []alert.Sismo

	// Match <tr> elements
	trReg := regexp.MustCompile(`(?si)<tr>(.*?)</tr>`)
	// Match <td> elements
	tdReg := regexp.MustCompile(`(?si)<td[^>]*>(.*?)</td>`)
	// Match any html tags to clean them
	tagReg := regexp.MustCompile(`<[^>]*>`)

	rows := trReg.FindAllStringSubmatch(html, -1)
	if len(rows) == 0 {
		return nil, fmt.Errorf("could not find tr elements in HTML")
	}

	for _, r := range rows {
		cellContent := r[1]
		cells := tdReg.FindAllStringSubmatch(cellContent, -1)
		
		if len(cells) < 6 {
			continue
		}

		var cols []string
		for _, c := range cells {
			txt := tagReg.ReplaceAllString(c[1], "")
			txt = strings.TrimSpace(txt)
			txt = strings.ReplaceAll(txt, "&nbsp;", " ")
			cols = append(cols, txt)
		}

		// Skip header rows
		if len(cols) > 0 {
			lowerCol := strings.ToLower(cols[0])
			if strings.Contains(lowerCol, "fecha") || strings.Contains(lowerCol, "sismo") || strings.Contains(lowerCol, "date") {
				continue
			}
		}

		// Columns: 0: Date (DD-MM-YYYY), 1: Time (HH:MM:SS), 2: Lat, 3: Lon, 4: Depth, 5: Mag, [6: Location]
		dateStr := cols[0]
		timeStr := cols[1]
		latStr := cols[2]
		lonStr := cols[3]
		depthStr := cols[4]
		magStr := cols[5]
		locStr := ""
		if len(cols) > 6 {
			locStr = cols[6]
		}

		sismo, err := s.parseRowData(dateStr, timeStr, latStr, lonStr, depthStr, magStr, locStr)
		if err != nil {
			continue // Skip row and keep going
		}
		events = append(events, sismo)
	}

	return events, nil
}

// ParseHTMLFallback is disabled to ensure failures are propagated.
func (s *FunvisisScraper) ParseHTMLFallback(html string) ([]alert.Sismo, error) {
	return nil, fmt.Errorf("fallback mock data generation is disabled to avoid silencing failures")
}

func (s *FunvisisScraper) parseRowData(dateStr, timeStr, latStr, lonStr, depthStr, magStr, locStr string) (alert.Sismo, error) {
	clean := func(val string) string {
		val = strings.ReplaceAll(val, ",", ".")
		val = regexp.MustCompile(`[^0-9.-]`).ReplaceAllString(val, "")
		return val
	}

	latVal, err := strconv.ParseFloat(clean(latStr), 64)
	if err != nil {
		return alert.Sismo{}, fmt.Errorf("latitude parsing: %w", err)
	}

	lonVal, err := strconv.ParseFloat(clean(lonStr), 64)
	if err != nil {
		return alert.Sismo{}, fmt.Errorf("longitude parsing: %w", err)
	}

	depthVal, _ := strconv.ParseFloat(clean(depthStr), 64)

	magClean := clean(magStr)
	if magClean == "" {
		m := regexp.MustCompile(`\d+(\.\d+)?`).FindString(magStr)
		if m != "" {
			magClean = m
		}
	}
	magVal, err := strconv.ParseFloat(magClean, 64)
	if err != nil {
		return alert.Sismo{}, fmt.Errorf("magnitude parsing: %w", err)
	}

	dateTimeStr := fmt.Sprintf("%s %s", strings.TrimSpace(dateStr), strings.TrimSpace(timeStr))
	var eventTime time.Time
	
	layouts := []string{
		"02-01-2006 15:04:05",
		"02/01/2006 15:04:05",
		"2006-01-02 15:04:05",
		"02-01-2006 15:04",
		"02/01/2006 15:04",
	}

	locHLV := time.FixedZone("HLV", -4*60*60)
	parsed := false
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, dateTimeStr, locHLV); err == nil {
			eventTime = t
			parsed = true
			break
		}
	}
	if !parsed {
		eventTime = time.Now().In(locHLV)
	}

	hashInput := fmt.Sprintf("%s-%s-%f-%f", dateStr, timeStr, latVal, lonVal)
	hasher := md5.New()
	hasher.Write([]byte(hashInput))
	eventID := "fun-" + hex.EncodeToString(hasher.Sum(nil))[:12]

	dist := geo.DistanceToLaGuaira(latVal, lonVal)
	if locStr == "" {
		locStr = "Venezuelan Region"
	}

	return alert.Sismo{
		ID:        eventID,
		Source:    "Funvisis",
		Magnitude: magVal,
		Depth:     depthVal,
		Latitude:  latVal,
		Longitude: lonVal,
		Location:  locStr,
		Time:      eventTime,
		Distance:  dist,
	}, nil
}

func (s *FunvisisScraper) log(format string, args ...interface{}) {
	if s.logger != nil {
		s.logger(format, args...)
	}
}

type FunvisisGeoJSON struct {
	Type     string            `json:"type"`
	Features []FunvisisFeature `json:"features"`
}

type FunvisisFeature struct {
	Type       string             `json:"type"`
	Geometry   FunvisisGeometry   `json:"geometry"`
	Properties FunvisisProperties `json:"properties"`
}

type FunvisisGeometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

type FunvisisProperties struct {
	PhoneFormatted string `json:"phoneFormatted"`
	Phone          string `json:"phone"` // Magnitude
	Address        string `json:"address"`
	City           string `json:"city"`       // Time HH:MM
	PostalCode     string `json:"postalCode"` // Date DD-MM-YYYY
	State          string `json:"state"`      // Depth
	Lat            string `json:"lat"`
	Long           string `json:"long"`
}

// ParseJSON parses the GeoJSON payload returned by Funvisis.
func (s *FunvisisScraper) ParseJSON(jsonStr string) ([]alert.Sismo, error) {
	var geoJSON FunvisisGeoJSON
	if err := json.Unmarshal([]byte(jsonStr), &geoJSON); err != nil {
		return nil, fmt.Errorf("unmarshal GeoJSON failed: %w", err)
	}

	if len(geoJSON.Features) == 0 {
		return nil, fmt.Errorf("no features found in GeoJSON")
	}

	var events []alert.Sismo
	for _, f := range geoJSON.Features {
		props := f.Properties

		dateStr := props.PostalCode
		timeStr := props.City
		latStr := props.Lat
		lonStr := props.Long
		depthStr := props.State
		magStr := props.Phone
		locStr := props.Address

		// Fallback to geometry coordinates if properties are empty
		if latStr == "" || lonStr == "" {
			if len(f.Geometry.Coordinates) >= 2 {
				lonStr = fmt.Sprintf("%f", f.Geometry.Coordinates[0])
				latStr = fmt.Sprintf("%f", f.Geometry.Coordinates[1])
			}
		}

		sismo, err := s.parseRowData(dateStr, timeStr, latStr, lonStr, depthStr, magStr, locStr)
		if err != nil {
			continue // Skip single bad event
		}
		events = append(events, sismo)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no events could be parsed from GeoJSON features")
	}

	return events, nil
}
