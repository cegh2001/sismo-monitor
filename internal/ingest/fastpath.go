package ingest

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// FamilyLocation is a single point of interest for which the fast path computes
// P-wave and S-wave arrival times. Configured via
// EMSC_FASTPATH_FAMILY_LOCATIONS as `lat,lon,name;lat,lon,name;…`.
type FamilyLocation struct {
	Name string
	Lat  float64
	Lon  float64
}

// FastPath consumes seismic events from a dedicated channel and dispatches an
// immediate Pushover alert within 2s of WebSocket receipt when the gate
// conditions are met: enabled, inside the Venezuela bounding box, magnitude
// above the threshold, and rate-limit cooldown elapsed. The notifier's own
// rate limiter is bypassed — FastPath enforces its own cooldown so early
// warnings are not throttled by pipeline traffic.
// FastPathNotifier defines the interface required by FastPath for immediate dispatch.
type FastPathNotifier interface {
	SendNow(alert alert.Alert) error
}

type FastPath struct {
	enabled      bool
	magThreshold float64
	rateLimit    time.Duration
	familyLocs   []FamilyLocation
	notifier     FastPathNotifier
	logger       func(string, ...interface{})

	mu       sync.Mutex
	lastSent time.Time
}

// ParseFamilyLocations parses a semicolon-separated string of `lat,lon,name`
// triples into a slice of FamilyLocation. Empty input returns nil. Entries
// with non-numeric lat/lon or fewer than three comma-separated parts are
// silently skipped. Whitespace around each field is trimmed.
//
// Exposed as a public helper so callers (e.g. cmd/monitor) can convert the
// raw config slice into a parsed form before constructing a FastPath.
func ParseFamilyLocations(raw string) []FamilyLocation {
	return parseFamilyLocations(raw)
}

// parseFamilyLocations is the package-private worker.
func parseFamilyLocations(raw string) []FamilyLocation {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ";")
	locs := make([]FamilyLocation, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fields := strings.Split(p, ",")
		if len(fields) < 3 {
			continue
		}
		lat, errLat := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		lon, errLon := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if errLat != nil || errLon != nil {
			continue
		}
		name := strings.TrimSpace(fields[2])
		if name == "" {
			continue
		}
		locs = append(locs, FamilyLocation{Name: name, Lat: lat, Lon: lon})
	}
	return locs
}

// NewFastPath constructs a FastPath from the supplied notifier. The notifier
// must be non-nil; FastPath relies on SendNow() to bypass the queue and the
// notifier's rate limiter.
func NewFastPath(enabled bool, magThreshold float64, rateLimit time.Duration, familyLocs []FamilyLocation, notifier FastPathNotifier, logger func(string, ...interface{})) *FastPath {
	return &FastPath{
		enabled:      enabled,
		magThreshold: magThreshold,
		rateLimit:    rateLimit,
		familyLocs:   familyLocs,
		notifier:     notifier,
		logger:       logger,
	}
}

// Process evaluates the fast-path gate for a single event. When all gates
// pass, it dispatches an [EARLY WARNING] alert via SendNow and updates the
// cooldown timestamp. If the event is suppressed by the rate limiter, no
// alert is sent (and a [FASTPATH] log line is emitted).
func (fp *FastPath) Process(event alert.Sismo) {
	if fp == nil {
		return
	}
	if !fp.enabled {
		return
	}
	if event.Latitude < geo.MinLat || event.Latitude > geo.MaxLat ||
		event.Longitude < geo.MinLon || event.Longitude > geo.MaxLon {
		return
	}
	if event.Magnitude < fp.magThreshold {
		return
	}

	fp.mu.Lock()
	if !fp.lastSent.IsZero() {
		elapsed := time.Since(fp.lastSent)
		if elapsed < fp.rateLimit {
			remaining := fp.rateLimit - elapsed
			fp.mu.Unlock()
			if fp.logger != nil {
				fp.logger("[FASTPATH] Rate-limited, waiting %s", remaining.Round(time.Millisecond))
			}
			return
		}
	}
	fp.lastSent = time.Now()
	fp.mu.Unlock()

	body := fp.buildBody(event)
	a := alert.Alert{
		Sismo:        event,
		Level:        alert.LevelPreAlert,
		EarlyWarning: true,
		Body:         body,
	}
	if fp.notifier == nil {
		return
	}
	if err := fp.notifier.SendNow(a); err != nil && fp.logger != nil {
		fp.logger("[FASTPATH] SendNow error: %v", err)
	}
}

// buildBody composes the [EARLY WARNING] message body, including P-wave and
// S-wave ETA for every configured family location. The body always ends with
// a "— pending classification" disclaimer so the receiver knows the alert
// has not yet been through the dedup/classify pipeline.
func (fp *FastPath) buildBody(event alert.Sismo) string {
	origin := event.Time
	if origin.IsZero() {
		origin = time.Now()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Magnitude: %.1f Mw\n", event.Magnitude))
	if event.Depth > 0 {
		sb.WriteString(fmt.Sprintf("Depth: %.1f km\n", event.Depth))
	}
	sb.WriteString(fmt.Sprintf("Origin: %s\n", origin.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Location: %s\n\n", event.Location))

	if len(fp.familyLocs) == 0 {
		sb.WriteString("— pending classification")
		return sb.String()
	}

	sb.WriteString("ETA P/S waves:\n")
	for _, loc := range fp.familyLocs {
		dist := geo.HaversineDistance(event.Latitude, event.Longitude, loc.Lat, loc.Lon)
		pTravel := time.Duration(dist / 6.0 * float64(time.Second))
		sTravel := time.Duration(dist / 3.5 * float64(time.Second))
		pArr := origin.Add(pTravel)
		sArr := origin.Add(sTravel)
		now := time.Now()
		pStr := "ya llegó"
		if pArr.After(now) {
			pStr = fmt.Sprintf("en %ds", int(pArr.Sub(now).Seconds()))
		}
		sStr := "ya llegó"
		if sArr.After(now) {
			sStr = fmt.Sprintf("en %ds", int(sArr.Sub(now).Seconds()))
		}
		sb.WriteString(fmt.Sprintf("  - %s (%.0f km): Onda P %s | Onda S %s\n", loc.Name, dist, pStr, sStr))
	}
	sb.WriteString("\n— pending classification")
	return sb.String()
}

// Start consumes events from fastOut and dispatches them via Process. The
// loop exits when ctx is cancelled. fastOut may be nil only if Process is
// called directly — Start requires a non-nil channel.
func (fp *FastPath) Start(ctx context.Context, fastOut <-chan alert.Sismo) {
	if fastOut == nil {
		if fp.logger != nil {
			fp.logger("[FASTPATH] Start called with nil channel; exiting")
		}
		return
	}
	for {
		select {
		case <-ctx.Done():
			if fp.logger != nil {
				fp.logger("[FASTPATH] Stopping")
			}
			return
		case event, ok := <-fastOut:
			if !ok {
				return
			}
			fp.Process(event)
		}
	}
}
