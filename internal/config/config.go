package config

import (
	"bufio"
	"os"
	"strings"
)

// Config holds configuration parameters for the seismic monitor.
type Config struct {
	PushoverAppToken  string
	PushoverUserKey   string
	Port              string
	USGSRealtimeURL   string
	USGSHistoricalURL string
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

	return &Config{
		PushoverAppToken:  os.Getenv("PUSHOVER_APP_TOKEN"),
		PushoverUserKey:   os.Getenv("PUSHOVER_USER_KEY"),
		Port:              port,
		USGSRealtimeURL:   usgsRealtime,
		USGSHistoricalURL: usgsHistorical,
	}
}
