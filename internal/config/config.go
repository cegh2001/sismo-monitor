package config

import (
	"os"
)

// Config holds configuration parameters for the seismic monitor.
type Config struct {
	PushoverAppToken  string
	PushoverUserKey   string
	Port              string
	USGSRealtimeURL   string
	USGSHistoricalURL string
}

// Load loads the configuration from environment variables.
func Load() *Config {
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
