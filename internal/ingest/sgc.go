package ingest

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"sismo-monitor/internal/alert"
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
	if s.cbState == cbOpen {
		if time.Since(s.cbOpenedAt) < s.cooldownPeriod {
			return nil, fmt.Errorf("circuit breaker OPEN: cooling down (%v remaining)",
				s.cooldownPeriod-time.Since(s.cbOpenedAt).Round(time.Second))
		}
		s.cbState = cbHalfOpen
		s.log("SGC circuit breaker: HALF_OPEN — attempting test request")
	}

	// Create chromedp context
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
		chromedp.WaitVisible(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Click(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click(".leaflet-control-zoom-out", chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),

		chromedp.SendKeys(`input[name="textFieldSearchEvents"]`, "Venezuela"),
		chromedp.Sleep(2*time.Second),
		chromedp.Evaluate(sgcExtractScript(), &resultJSON),
	)

	if err != nil {
		s.recordFailure()
		return nil, fmt.Errorf("chromedp extraction failed: %w", err)
	}

	// Parse the JSON result
	events, parseErr := parseExtractResult(resultJSON, s.log)
	if parseErr != nil {
		s.recordFailure()
		return nil, fmt.Errorf("extract result parse failed: %w (possible UI change)", parseErr)
	}

	if len(events) == 0 {
		s.recordFailure()
		return nil, fmt.Errorf("extracted 0 events — possible UI change or no Venezuela events")
	}

	// Validate data quality
	if err := validateEvents(events); err != nil {
		s.recordFailure()
		return nil, fmt.Errorf("event validation failed: %w (possible format change)", err)
	}

	// Success — reset circuit breaker
	s.resetCircuitBreaker()
	return events, nil
}

func (s *SGCScraper) log(format string, args ...interface{}) {
	if s.logger != nil {
		s.logger(format, args...)
	}
}
