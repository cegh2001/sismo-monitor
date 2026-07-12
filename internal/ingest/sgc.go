package ingest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"regexp"
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
	cbClosed    = "closed"
	cbOpen      = "open"
	cbHalfOpen  = "half_open"
)

// SGCScraper polls the SGC Colombia web portal using a headless browser,
// extracting seismic events for the Venezuela/Colombia region.
type SGCScraper struct {
	pollInterval   time.Duration
	logger         func(string, ...interface{})
	seenEvents     map[string]time.Time
	mu             sync.Mutex

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

	// Create chromedp context with a timeout
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 900),
	)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	// Timeout for the entire scrape operation
	scrapeCtx, scrapeCancel := context.WithTimeout(taskCtx, 30*time.Second)
	defer scrapeCancel()

	// --- DOM extraction ---
	var tableHTML string
	var pageTitle string

	err := chromedp.Run(scrapeCtx,
		chromedp.Navigate("https://www.sgc.gov.co/sismos"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Sleep(3*time.Second), // Wait for React to render
		chromedp.Title(&pageTitle),
		// Try multiple selectors for the event table
		chromedp.ActionFunc(func(ctx context.Context) error {
			return s.extractTable(ctx, &tableHTML)
		}),
	)

	if err != nil {
		s.recordFailure()
		return nil, fmt.Errorf("chromedp navigation/extraction failed: %w", err)
	}

	if tableHTML == "" {
		s.recordFailure()
		return nil, fmt.Errorf("no event table found on page (title: %q) — possible UI change detected: expected table with seismic events", pageTitle)
	}

	// Parse the extracted HTML
	events, parseErr := s.parseTableHTML(tableHTML)
	if parseErr != nil {
		s.recordFailure()
		return nil, fmt.Errorf("table parsing failed: %w (possible UI structure change)", parseErr)
	}

	if len(events) == 0 {
		s.recordFailure()
		return nil, fmt.Errorf("parsed 0 events from table — possible UI change: expected event rows with date/magnitude/location columns")
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

// extractTable tries multiple selector strategies to find the seismic event table.
func (s *SGCScraper) extractTable(ctx context.Context, tableHTML *string) error {
	// Strategy 1: Look for table elements with seismic data patterns
	selectors := []string{
		"table",                          // any table
		"[role='grid']",                  // MUI DataGrid / AG Grid
		"[class*='MuiTable']",            // MUI Table
		"[class*='table']",               // generic table class
		"[class*='sismo']",               // sismo-related class
		"[class*='evento']",              // evento-related class
		".MuiTableContainer",             // MUI Table container
		"div[class*='MuiDataGrid']",      // MUI DataGrid
	}

	var foundSelector string
	for _, sel := range selectors {
		var html string
		err := chromedp.Run(ctx,
			chromedp.OuterHTML(sel, &html, chromedp.ByQuery),
		)
		if err == nil && len(html) > 100 {
			// Check if this looks like a data table (contains numbers)
			if s.looksLikeDataTable(html) {
				*tableHTML = html
				foundSelector = sel
				break
			}
		}
	}

	if foundSelector == "" {
		// Last resort: grab the entire body and let the parser figure it out
		return chromedp.Run(ctx, chromedp.OuterHTML("body", tableHTML, chromedp.ByQuery))
	}

	// Validate against last known good selector
	if s.lastValidSelector != "" && foundSelector != s.lastValidSelector {
		s.log("SGC UI CHANGE DETECTED: table selector changed from %q to %q", s.lastValidSelector, foundSelector)
	}
	s.lastValidSelector = foundSelector
	return nil
}

// looksLikeDataTable checks if HTML content appears to be a table with numeric data.
func (s *SGCScraper) looksLikeDataTable(html string) bool {
	// Count table row patterns
	trCount := strings.Count(html, "<tr") + strings.Count(html, "<div role=\"row\"")
	tdCount := strings.Count(html, "<td") + strings.Count(html, "<div role=\"gridcell\"")
	// Check for numeric patterns (lat/lon/mag look like numbers)
	numPattern := regexp.MustCompile(`[>]\s*-?\d+\.?\d*\s*[<]`)
	numMatches := len(numPattern.FindAllString(html, -1))
	return trCount >= 2 && tdCount >= 6 && numMatches >= 5
}

// parseTableHTML extracts seismic event data from HTML.
func (s *SGCScraper) parseTableHTML(html string) ([]alert.Sismo, error) {
	var events []alert.Sismo

	// Strategy: extract rows, then cells, then parse columns
	// Try multiple row extraction patterns

	// Pattern 1: Standard <tr> rows
	trPattern := regexp.MustCompile(`(?si)<tr[^>]*>(.*?)</tr>`)
	rows := trPattern.FindAllStringSubmatch(html, -1)

	// Pattern 2: Div-based grid rows (MUI DataGrid, AG Grid)
	if len(rows) < 2 {
		divRowPattern := regexp.MustCompile(`(?si)<div[^>]*role="row"[^>]*>(.*?)</div>`)
		rows = divRowPattern.FindAllStringSubmatch(html, -1)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("no table rows found in HTML (%d bytes)", len(html))
	}

	// Skip header row(s)
	startIdx := 0
	for i, r := range rows {
		content := r[1]
		lower := strings.ToLower(content)
		if strings.Contains(lower, "fecha") || strings.Contains(lower, "magnitud") ||
			strings.Contains(lower, "date") || strings.Contains(lower, "mag") ||
			strings.Contains(lower, "hora") || strings.Contains(lower, "lat") ||
			strings.Contains(lower, "profundidad") {
			startIdx = i + 1
		}
	}

	// Extract cells from each row
	tdPattern := regexp.MustCompile(`(?si)<t[dh][^>]*>(.*?)</t[dh]>`)
	divCellPattern := regexp.MustCompile(`(?si)<div[^>]*role="gridcell"[^>]*>(.*?)</div>`)
	tagPattern := regexp.MustCompile(`<[^>]*>`)
	wsPattern := regexp.MustCompile(`\s+`)

	for i := startIdx; i < len(rows); i++ {
		cellContent := rows[i][1]

		// Try <td>/<th> first, then div grid cells
		cells := tdPattern.FindAllStringSubmatch(cellContent, -1)
		if len(cells) == 0 {
			cells = divCellPattern.FindAllStringSubmatch(cellContent, -1)
		}

		if len(cells) < 5 {
			continue // Not enough columns
		}

		// Clean cell text
		var cols []string
		for _, c := range cells {
			txt := tagPattern.ReplaceAllString(c[1], " ")
			txt = wsPattern.ReplaceAllString(txt, " ")
			txt = strings.TrimSpace(txt)
			txt = strings.ReplaceAll(txt, "&nbsp;", " ")
			txt = strings.ReplaceAll(txt, "\n", " ")
			if txt != "" {
				cols = append(cols, txt)
			}
		}

		if len(cols) < 5 {
			continue
		}

		// Try to find the column mapping by analyzing header-less data
		sismo, err := s.parseSGCRow(cols)
		if err != nil {
			continue
		}
		events = append(events, sismo)
	}

	if len(events) == 0 {
		// As a diagnostic, log the first non-header row content
		if len(rows) > startIdx {
			cleanRow := tagPattern.ReplaceAllString(rows[startIdx][1], " | ")
			s.log("SGC DEBUG: first data row (cleaned): %s", strings.TrimSpace(cleanRow)[:min(len(cleanRow), 200)])
		}
	}

	return events, nil
}

// parseSGCRow attempts to parse a row of cleaned column text into a Sismo.
// Since we don't know the exact column order, we try to identify columns by content patterns.
func (s *SGCScraper) parseSGCRow(cols []string) (alert.Sismo, error) {
	// Expected columns (order may vary): Date, Time, Latitude, Longitude, Depth, Magnitude, [Location]
	// We identify them by pattern matching

	var dateStr, timeStr, latStr, lonStr, depthStr, magStr, locStr string
	var foundDate, foundTime, foundLat, foundLon, foundDepth, foundMag bool

	dateRegex := regexp.MustCompile(`^\d{2}[-/]\d{2}[-/]\d{4}$|^\d{4}[-/]\d{2}[-/]\d{2}$`)
	timeRegex := regexp.MustCompile(`^\d{2}:\d{2}(:\d{2})?$`)
	coordRegex := regexp.MustCompile(`^-?\d{1,2}\.\d{2,}$`)
	depthRegex := regexp.MustCompile(`^\d{1,3}(\.\d+)?$`)
	magRegex := regexp.MustCompile(`^\d\.\d$|^\d\.\d\d?$`)

	for _, col := range cols {
		col = strings.TrimSpace(col)
		switch {
		case !foundDate && dateRegex.MatchString(col):
			dateStr = col
			foundDate = true
		case !foundTime && timeRegex.MatchString(col):
			timeStr = col
			foundTime = true
		case !foundLat && coordRegex.MatchString(col):
			// Check if value is in lat range (-5 to 13 for Colombia/Venezuela)
			if val, err := strconv.ParseFloat(col, 64); err == nil {
				if val >= -5 && val <= 13 {
					latStr = col
					foundLat = true
				} else if !foundLon && val >= -80 && val <= -59 {
					lonStr = col
					foundLon = true
				}
			}
		case !foundLon && coordRegex.MatchString(col):
			if val, err := strconv.ParseFloat(col, 64); err == nil {
				if val >= -80 && val <= -59 {
					lonStr = col
					foundLon = true
				}
			}
		case !foundDepth && depthRegex.MatchString(col):
			if val, err := strconv.ParseFloat(col, 64); err == nil {
				if val >= 0 && val <= 300 {
					depthStr = col
					foundDepth = true
				}
			}
		case !foundMag && magRegex.MatchString(col):
			magStr = col
			foundMag = true
		case len(col) > 3 && !strings.ContainsAny(col, "0123456789.-"):
			// Looks like a location name
			if locStr == "" {
				locStr = col
			}
		}
	}

	// Must have at least date, mag, and one coordinate
	if !foundMag {
		return alert.Sismo{}, fmt.Errorf("could not identify magnitude in row: %v", cols)
	}
	if !foundLat && !foundLon {
		return alert.Sismo{}, fmt.Errorf("could not identify coordinates in row: %v", cols)
	}

	// Parse values
	latVal, _ := strconv.ParseFloat(strings.ReplaceAll(latStr, ",", "."), 64)
	lonVal, _ := strconv.ParseFloat(strings.ReplaceAll(lonStr, ",", "."), 64)
	depthVal, _ := strconv.ParseFloat(strings.ReplaceAll(depthStr, ",", "."), 64)
	magVal, err := strconv.ParseFloat(strings.ReplaceAll(magStr, ",", "."), 64)
	if err != nil {
		return alert.Sismo{}, fmt.Errorf("magnitude parse error: %w", err)
	}

	// Parse date/time
	var eventTime time.Time
	locHLV := time.FixedZone("HLV", -5*60*60) // Colombia timezone
	if foundDate {
		dateTimeStr := dateStr
		if foundTime {
			dateTimeStr = dateStr + " " + timeStr
		}
		layouts := []string{
			"02-01-2006 15:04:05",
			"02/01/2006 15:04:05",
			"2006-01-02 15:04:05",
			"2006/01/02 15:04:05",
			"02-01-2006 15:04",
			"02/01/2006 15:04",
			"2006-01-02",
			"2006/01/02",
		}
		for _, l := range layouts {
			if t, err := time.ParseInLocation(l, dateTimeStr, locHLV); err == nil {
				eventTime = t
				break
			}
		}
	}
	if eventTime.IsZero() {
		eventTime = time.Now().In(locHLV)
	}

	// Generate stable ID
	hashInput := fmt.Sprintf("sgc-%s-%s-%.3f-%.3f-%.1f", dateStr, timeStr, latVal, lonVal, magVal)
	hasher := md5.New()
	hasher.Write([]byte(hashInput))
	eventID := "sgc-" + hex.EncodeToString(hasher.Sum(nil))[:12]

	dist := geo.DistanceToLaGuaira(latVal, lonVal)
	if locStr == "" {
		locStr = "Colombia/Venezuela Region"
	}

	gridCell := geo.GetGridCell(latVal, lonVal)
	if gridCell == "OUT_OF_BOUNDS" {
		// Still include it — SGC events are relevant even if outside our grid
		gridCell = "REGIONAL"
	}

	return alert.Sismo{
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
	}, nil
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
