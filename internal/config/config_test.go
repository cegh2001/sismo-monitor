package config

import (
	"os"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	// Create temporary .env file
	envContent := "PUSHOVER_APP_TOKEN=mock_app_token\nPUSHOVER_USER_KEY=\"mock_user_key\"\n"
	err := os.WriteFile(".env", []byte(envContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create temporary .env file: %v", err)
	}
	defer os.Remove(".env")

	cfg := Load()

	if cfg.PushoverAppToken != "mock_app_token" {
		t.Errorf("Expected PushoverAppToken 'mock_app_token', got %q", cfg.PushoverAppToken)
	}
	if cfg.PushoverUserKey != "mock_user_key" {
		t.Errorf("Expected PushoverUserKey 'mock_user_key', got %q", cfg.PushoverUserKey)
	}
}
