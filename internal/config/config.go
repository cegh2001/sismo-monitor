package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Config holds configuration parameters for the seismic monitor.
type Config struct {
	PushoverAppToken  string
	PushoverUserKey   string
	AlertProvider     string // "gotify", "pushover", or "none"
	GotifyURL         string
	GotifyAppToken    string
	GeminiAPIKey      string
	Port              string
	USGSRealtimeURL   string
	USGSHistoricalURL string

	// EMSC Fast-Path Early Warning fields
	EMSCFastPathEnabled        bool
	EMSCFastPathMagThreshold   float64
	EMSCFastPathRateLimitSec   int
	EMSCFastPathFamilyLocations []string
}

// loadDotEnv reads key-value pairs from a local .env file and injects them into environment variables.
func loadDotEnv() {
	file, err := os.Open(".env")
	if err != nil {
		return // Silently skip if file does not exist
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		// Strip surrounding quotes
		val = strings.Trim(val, `"'`)

		os.Setenv(key, val)
	}
}

// Load loads the configuration from environment variables.
func Load() *Config {
	loadDotEnv()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	usgsRealtime := os.Getenv("USGS_REALTIME_URL")
	if usgsRealtime == "" {
		usgsRealtime = "https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/all_hour.geojson"
	}
	usgsHistorical := os.Getenv("USGS_HISTORICAL_URL")
	if usgsHistorical == "" {
		usgsHistorical = "https://earthquake.usgs.gov/fdsnws/event/1/query"
	}

	fastPathEnabled := true
	if v := os.Getenv("EMSC_FASTPATH_ENABLED"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err == nil {
			fastPathEnabled = parsed
		}
	}

	fastPathMagThreshold := 4.5
	if v := os.Getenv("EMSC_FASTPATH_MAG_THRESHOLD"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			fastPathMagThreshold = parsed
		}
	}

	fastPathRateLimitSec := 10
	if v := os.Getenv("EMSC_FASTPATH_RATE_LIMIT_SEC"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			fastPathRateLimitSec = parsed
		}
	}

	fastPathFamilyLocations := []string{"10.60,-66.93,LaGuaira"}
	if v := os.Getenv("EMSC_FASTPATH_FAMILY_LOCATIONS"); v != "" {
		parts := strings.Split(v, ";")
		locs := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				locs = append(locs, p)
			}
		}
		if len(locs) > 0 {
			fastPathFamilyLocations = locs
		}
	}

	alertProvider := strings.ToLower(strings.TrimSpace(os.Getenv("ALERT_PROVIDER")))
	gotifyURL := os.Getenv("GOTIFY_URL")
	gotifyToken := os.Getenv("GOTIFY_APP_TOKEN")
	if alertProvider == "" {
		if gotifyURL != "" || gotifyToken != "" {
			alertProvider = "gotify"
		} else {
			alertProvider = "pushover"
		}
	}

	return &Config{
		PushoverAppToken:            os.Getenv("PUSHOVER_APP_TOKEN"),
		PushoverUserKey:             os.Getenv("PUSHOVER_USER_KEY"),
		AlertProvider:              alertProvider,
		GotifyURL:                  gotifyURL,
		GotifyAppToken:             gotifyToken,
		GeminiAPIKey:                os.Getenv("GEMINI_API_KEY"),
		Port:                        port,
		USGSRealtimeURL:             usgsRealtime,
		USGSHistoricalURL:           usgsHistorical,
		EMSCFastPathEnabled:         fastPathEnabled,
		EMSCFastPathMagThreshold:    fastPathMagThreshold,
		EMSCFastPathRateLimitSec:    fastPathRateLimitSec,
		EMSCFastPathFamilyLocations: fastPathFamilyLocations,
	}
}
