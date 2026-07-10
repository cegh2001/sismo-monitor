package config

import (
	"os"
)

// Config holds configuration parameters for the seismic monitor.
type Config struct {
	PushoverAppToken string
	PushoverUserKey  string
	Port             string
}

// Load loads the configuration from environment variables.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &Config{
		PushoverAppToken: os.Getenv("PUSHOVER_APP_TOKEN"),
		PushoverUserKey:  os.Getenv("PUSHOVER_USER_KEY"),
		Port:             port,
	}
}
