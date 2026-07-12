package ingest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// Circuit breaker states.
const (
	cbClosed   = "closed"
	cbOpen     = "open"
	cbHalfOpen = "half_open"
)

// SGCScraper polls the SGC Colombia web portal using a headless browser,
// extracting seismic events for the Venezuela/Colombia region.
type SGCScraper struct {
	pollInterval time.Duration
	logger       func(string, ...interface{})
	seenEvents   map[string]time.Time
	mu           sync.Mutex

	// Circuit breaker
	cbState          string
	consecutiveFails int
	cbOpenedAt       time.Time
	cooldownPeriod   time.Duration
	maxFails         int

	// DOM structure fingerprint (for detecting UI changes)
	lastValidSelector string
	lastColumnCount   int
}

// NewSGCScraper creates a new SGCScraper.
func NewSGCScraper(logger func(string, ...interface{})) *SGCScraper {
	return &SGCScraper{
		pollInterval:   120 * time.Second,
		logger:         logger,
		seenEvents:     make(map[string]time.Time),
		cbState:        cbClosed,
		cooldownPeriod: 5 * time.Minute,
		maxFails:       5,
	}
}

// Start runs the polling loop.
func (s *SGCScraper) Start(ctx context.Context, out chan<- alert.Sismo) {
	s.log("SGC scraper starting. Interval: %v", s.pollInterval)

	// First scrape immediately
	s.scrapeAndDispatch(ctx, out)

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log("SGC scraper exiting.")
			return
		case <-ticker.C:
			s.scrapeAndDispatch(ctx, out)
		}
	}
}

func (s *SGCScraper) scrapeAndDispatch(ctx context.Context, out chan<- alert.Sismo) {
	events, err := s.Scrape(ctx)
	if err != nil {
		s.log("SGC scrape failed: %v", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.seenEvents == nil {
		s.seenEvents = make(map[string]time.Time)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	for id, addedAt := range s.seenEvents {
		if addedAt.Before(cutoff) {
			delete(s.seenEvents, id)
		}
	}

	var newEvents []alert.Sismo
	for _, e := range events {
		if _, seen := s.seenEvents[e.ID]; !seen {
			s.seenEvents[e.ID] = time.Now()
			newEvents = append(newEvents, e)
		}
	}

	sort.Slice(newEvents, func(i, j int) bool {
		return newEvents[i].Time.Before(newEvents[j].Time)
	})

	s.log("SGC scraper found %d events (%d new).", len(events), len(newEvents))
	for _, e := range newEvents {
		select {
		case out <- e:
		default:
		}
	}
}

// Scrape uses chromedp to load the SGC page and extract event data.
func (s *SGCScraper) Scrape(ctx context.Context) ([]alert.Sismo, error) {
	// --- Circuit breaker gate ---
	switch s.cbState {
	case cbOpen:
		if time.Since(s.cbOpenedAt) < s.cooldownPeriod {
			return nil, fmt.Errorf("circuit breaker OPEN: cooling down (%v remaining)",
				s.cooldownPeriod-time.Since(s.cbOpenedAt).Round(time.Second))
		}
		s.cbState = cbHalfOpen
		s.log("SGC circuit breaker: HALF_OPEN — attempting test request")
	case cbHalfOpen:
		// will be resolved after this attempt
	}

	// Create chromedp context (headless by default in DefaultExecAllocatorOptions)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1280, 900),
		chromedp.Flag("disable-web-security", true),
	)

	// Detect Chrome path on Windows
	chromePaths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		chromePaths = append(chromePaths,
			localAppData+`\Google\Chrome\Application\chrome.exe`)
	}
	for _, p := range chromePaths {
		if _, err := os.Stat(p); err == nil {
			opts = append(opts, chromedp.ExecPath(p))
			break
		}
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	s.log("SGC: launching headless browser...")
	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	// Timeout for the entire scrape operation
	scrapeCtx, scrapeCancel := context.WithTimeout(taskCtx, 90*time.Second)
	defer scrapeCancel()

	var resultJSON string
	var pageTitle string

	s.log("SGC: navigating to sgc.gov.co/sismos...")
	err := chromedp.Run(scrapeCtx,
		chromedp.Navigate("https://www.sgc.gov.co/sismos"),
		chromedp.WaitVisible(".item-container", chromedp.ByQueryAll),
		chromedp.Title(&pageTitle),
		chromedp.ActionFunc(func(ctx context.Context) error {
			s.log("SGC: page loaded (title: %s), zooming out map for regional events...", pageTitle)
			return nil
		}),
		// Zoom out Leaflet map to expand viewport and load regional events
		chromedp.WaitVisible(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Click(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Sleep(1*time.Second), // Let the map reload tiles and events

		// Type "Venezuela" in search input to filter events
		chromedp.SendKeys(`input[name="textFieldSearchEvents"]`, "Venezuela"),
		chromedp.Sleep(2*time.Second), // Wait for React filter to apply
		// Single JS evaluation: extract visible items
		chromedp.Evaluate(sgcExtractScript(), &resultJSON),
	)

	if err != nil {
		s.recordFailure()
		return nil, fmt.Errorf("chromedp extraction failed: %w", err)
	}

	// Parse the JSON result from the JS script
	events, parseErr := s.parseExtractResult(resultJSON)
	if parseErr != nil {
		s.recordFailure()
		return nil, fmt.Errorf("extract result parse failed: %w (possible UI change)", parseErr)
	}

	if len(events) == 0 {
		s.recordFailure()
		return nil, fmt.Errorf("extracted 0 events — possible UI change or no Venezuela events")
	}

	// Validate data quality
	if err := s.validateEvents(events); err != nil {
		s.recordFailure()
		return nil, fmt.Errorf("event validation failed: %w (possible format change)", err)
	}

	// Success — reset circuit breaker
	s.resetCircuitBreaker()
	return events, nil
}

// sgcExtractScript returns JavaScript that extracts filtered seismic card data.
func sgcExtractScript() string {
	return `
(function() {
	var items = document.querySelectorAll('div.item');
	var result = [];
	for (var i = 0; i < items.length; i++) {
		var el = items[i];
		// Skip if the element is not visible (filtered out by React)
		if (el.offsetParent === null) continue;

		var idAttr = el.getAttribute('id') || '';
		if (!idAttr.startsWith('item')) continue;
		
		var sismoId = idAttr.substring(4);
		var contentEl = document.getElementById('item-content' + sismoId);
		
		var magEl = el.querySelector('.magnitude');
		var magnitude = magEl ? magEl.textContent.trim() : '';
		
		var placeEl = el.querySelector('.place');
		var place = placeEl ? placeEl.textContent.trim() : '';
		
		var dateEl = el.querySelector('.date-text');
		var dateTime = dateEl ? dateEl.textContent.trim() : '';
		
		var depthEl = el.querySelector('.depth');
		var depth = depthEl ? depthEl.textContent.trim() : '';
		
		var lat = '';
		var lon = '';
		var localizacionText = '';
		var nearby = '';
		
		if (contentEl) {
			var infoTexts = contentEl.querySelectorAll('.info-text');
			for (var j = 0; j < infoTexts.length; j++) {
				var txt = infoTexts[j].textContent.trim();
				if (txt.includes('Localización:') || txt.includes('Localizacion:')) {
					localizacionText = txt;
					var parts = txt.split(':');
					if (parts.length > 1) {
						var coords = parts[1].trim().split(',');
						if (coords.length === 2) {
							lat = coords[0].trim().replace('°', '').replace('?', '').trim();
							lon = coords[1].trim().replace('°', '').replace('?', '').trim();
						}
					}
				}
				if (txt.includes('Municipios cercanos:') || txt.includes('Municipios cercanos')) {
					nearby = txt;
				}
			}
		}
		
		if ((!lat || !lon) && contentEl) {
			var contentText = contentEl.textContent;
			var coordRegex = /(-?\d+\.\d+)\s*°?\s*,\s*(-?\d+\.\d+)\s*°?/;
			var match = contentText.match(coordRegex);
			if (match) {
				lat = match[1];
				lon = match[2];
			}
		}
		
		result.push({
			magnitude: magnitude,
			place: place,
			dateTime: dateTime,
			depth: depth,
			latitude: lat,
			longitude: lon,
			localizacionText: localizacionText,
			sismoId: sismoId,
			nearby: nearby
		});
	}
	return JSON.stringify(result);
})()
`
}

type sgcJSEvent struct {
	Magnitude        string `json:"magnitude"`
	Place            string `json:"place"`
	DateTime         string `json:"dateTime"`
	Depth            string `json:"depth"`
	Latitude         string `json:"latitude"`
	Longitude        string `json:"longitude"`
	LocalizacionText string `json:"localizacionText"`
	SismoID          string `json:"sismoId"`
	Nearby           string `json:"nearby"`
}

func (s *SGCScraper) parseExtractResult(jsonStr string) ([]alert.Sismo, error) {
	var jsEvents []sgcJSEvent
	if err := json.Unmarshal([]byte(jsonStr), &jsEvents); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	var events []alert.Sismo
	locHLV := time.FixedZone("HLV", -5*60*60) // Colombia timezone

	for _, jsEvent := range jsEvents {
		// Parse magnitude
		magStr := strings.TrimSuffix(jsEvent.Magnitude, "M")
		magVal, err := strconv.ParseFloat(magStr, 64)
		if err != nil {
			s.log("SGC: failed to parse magnitude %q: %v", jsEvent.Magnitude, err)
			continue
		}

		// Parse depth
		var depthVal float64
		depthClean := strings.ToLower(strings.TrimSuffix(jsEvent.Depth, "km"))
		if depthClean != "superficial" && depthClean != "" {
			depthVal, err = strconv.ParseFloat(depthClean, 64)
			if err != nil {
				s.log("SGC: failed to parse depth %q: %v", jsEvent.Depth, err)
			}
		}

		// Parse coordinates
		latVal, errLat := strconv.ParseFloat(jsEvent.Latitude, 64)
		lonVal, errLon := strconv.ParseFloat(jsEvent.Longitude, 64)
		if errLat != nil || errLon != nil {
			s.log("SGC: failed to parse coordinates (lat: %q, lon: %q): %v %v", jsEvent.Latitude, jsEvent.Longitude, errLat, errLon)
			continue
		}

		// Parse date/time
		var eventTime time.Time
		layouts := []string{
			"2006-01-02 15:04:05", "2006/01/02 15:04:05",
			"2006-01-02 15:04", "2006/01/02 15:04",
			"02-01-2006 15:04:05", "02/01/2006 15:04:05",
			"02-01-2006 15:04", "02/01/2006 15:04",
		}
		for _, l := range layouts {
			if t, err := time.ParseInLocation(l, jsEvent.DateTime, locHLV); err == nil {
				eventTime = t
				break
			}
		}
		if eventTime.IsZero() {
			s.log("SGC: failed to parse date/time %q, using now", jsEvent.DateTime)
			eventTime = time.Now().In(locHLV)
		}

		// Generate stable ID
		hashInput := fmt.Sprintf("sgc-%s-%.3f-%.3f-%.1f", jsEvent.DateTime, latVal, lonVal, magVal)
		hasher := md5.New()
		hasher.Write([]byte(hashInput))
		eventID := "sgc-" + hex.EncodeToString(hasher.Sum(nil))[:12]

		dist := geo.DistanceToLaGuaira(latVal, lonVal)
		locStr := jsEvent.Place
		if strings.ToLower(strings.TrimSpace(locStr)) == "venezuela" && jsEvent.Nearby != "" {
			locStr = parseSpecificLocation(locStr, jsEvent.Nearby)
		}
		if locStr == "" {
			locStr = "Colombia/Venezuela Region"
		}

		gridCell := geo.GetGridCell(latVal, lonVal)
		if gridCell == "OUT_OF_BOUNDS" {
			gridCell = "REGIONAL"
		}

		events = append(events, alert.Sismo{
			ID:        eventID,
			Source:    "SGC",
			Magnitude: magVal,
			Depth:     depthVal,
			Latitude:  latVal,
			Longitude: lonVal,
			Location:  locStr,
			Time:      eventTime,
			Distance:  dist,
			GridCell:  gridCell,
		})
	}

	return events, nil
}

// validateEvents checks that extracted events have reasonable values.
func (s *SGCScraper) validateEvents(events []alert.Sismo) error {
	for i, e := range events {
		if e.Magnitude <= 0 || e.Magnitude > 10 {
			return fmt.Errorf("event %d: implausible magnitude %.1f", i, e.Magnitude)
		}
		if e.Latitude < -90 || e.Latitude > 90 {
			return fmt.Errorf("event %d: implausible latitude %.3f", i, e.Latitude)
		}
		if e.Longitude < -180 || e.Longitude > 180 {
			return fmt.Errorf("event %d: implausible longitude %.3f", i, e.Longitude)
		}
	}
	return nil
}

// --- Circuit breaker ---

func (s *SGCScraper) recordFailure() {
	s.consecutiveFails++
	s.log("SGC circuit breaker: %d/%d consecutive failures", s.consecutiveFails, s.maxFails)
	if s.consecutiveFails >= s.maxFails {
		s.cbState = cbOpen
		s.cbOpenedAt = time.Now()
		s.log("SGC circuit breaker: OPEN — pausing requests for %v. Check SGC website for UI changes.", s.cooldownPeriod)
	}
}

func (s *SGCScraper) resetCircuitBreaker() {
	if s.cbState == cbHalfOpen {
		s.log("SGC circuit breaker: CLOSED — test request succeeded")
	} else if s.consecutiveFails > 0 {
		s.log("SGC circuit breaker: reset after %d failures", s.consecutiveFails)
	}
	s.consecutiveFails = 0
	s.cbState = cbClosed
}

func (s *SGCScraper) log(format string, args ...interface{}) {
	if s.logger != nil {
		s.logger(format, args...)
	}
}

// Helper to parse nearby municipalities list to a specific location string.
func parseSpecificLocation(place string, nearby string) string {
	place = strings.TrimSpace(place)
	nearby = strings.TrimSpace(nearby)
	if nearby == "" {
		return place
	}

	prefix := "Municipios cercanos:"
	if strings.HasPrefix(nearby, prefix) {
		nearby = strings.TrimSpace(strings.TrimPrefix(nearby, prefix))
	}

	parts := strings.Split(nearby, ",")
	if len(parts) == 0 || parts[0] == "" {
		return place
	}

	firstMun := strings.TrimSpace(parts[0])
	if strings.Contains(firstMun, "( Venezuela)") {
		firstMun = strings.ReplaceAll(firstMun, "( Venezuela)", ", Venezuela")
	}
	if strings.Contains(firstMun, " a ") {
		firstMun = strings.Replace(firstMun, " a ", " (", 1) + ")"
	}
	firstMun = strings.ReplaceAll(firstMun, " ,", ",")
	firstMun = strings.Join(strings.Fields(firstMun), " ")

	return firstMun
}
