package config

import (
	"os"
	"reflect"
	"sort"
	"testing"
)

func clearFastPathEnv(t *testing.T) {
	t.Helper()
	envs := []string{
		"EMSC_FASTPATH_ENABLED",
		"EMSC_FASTPATH_MAG_THRESHOLD",
		"EMSC_FASTPATH_RATE_LIMIT_SEC",
		"EMSC_FASTPATH_FAMILY_LOCATIONS",
		"PORT",
		"USGS_REALTIME_URL",
		"USGS_HISTORICAL_URL",
		"PUSHOVER_APP_TOKEN",
		"PUSHOVER_USER_KEY",
		"GEMINI_API_KEY",
	}
	for _, e := range envs {
		_ = os.Unsetenv(e)
	}
	_ = os.Remove(".env")
}

func TestLoadDotEnv(t *testing.T) {
	clearFastPathEnv(t)
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

func TestFastPathDefaults(t *testing.T) {
	clearFastPathEnv(t)
	cfg := Load()

	if !cfg.EMSCFastPathEnabled {
		t.Errorf("Expected EMSCFastPathEnabled default true, got false")
	}
	if cfg.EMSCFastPathMagThreshold != 4.5 {
		t.Errorf("Expected EMSCFastPathMagThreshold default 4.5, got %v", cfg.EMSCFastPathMagThreshold)
	}
	if cfg.EMSCFastPathRateLimitSec != 10 {
		t.Errorf("Expected EMSCFastPathRateLimitSec default 10, got %d", cfg.EMSCFastPathRateLimitSec)
	}
	got := append([]string{}, cfg.EMSCFastPathFamilyLocations...)
	sort.Strings(got)
	want := []string{"10.60,-66.93,LaGuaira"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expected EMSCFastPathFamilyLocations default %v, got %v", want, got)
	}
}

func TestFastPathEnvOverride(t *testing.T) {
	clearFastPathEnv(t)
	t.Setenv("EMSC_FASTPATH_ENABLED", "false")
	t.Setenv("EMSC_FASTPATH_MAG_THRESHOLD", "5.5")
	t.Setenv("EMSC_FASTPATH_RATE_LIMIT_SEC", "30")
	t.Setenv("EMSC_FASTPATH_FAMILY_LOCATIONS", "10.60,-66.93,LaGuaira;10.48,-66.90,Caracas")

	cfg := Load()

	if cfg.EMSCFastPathEnabled {
		t.Errorf("Expected EMSCFastPathEnabled false, got true")
	}
	if cfg.EMSCFastPathMagThreshold != 5.5 {
		t.Errorf("Expected EMSCFastPathMagThreshold 5.5, got %v", cfg.EMSCFastPathMagThreshold)
	}
	if cfg.EMSCFastPathRateLimitSec != 30 {
		t.Errorf("Expected EMSCFastPathRateLimitSec 30, got %d", cfg.EMSCFastPathRateLimitSec)
	}
	got := append([]string{}, cfg.EMSCFastPathFamilyLocations...)
	sort.Strings(got)
	want := []string{"10.48,-66.90,Caracas", "10.60,-66.93,LaGuaira"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Expected EMSCFastPathFamilyLocations %v, got %v", want, got)
	}
}

func TestGeminiAPIKey(t *testing.T) {
	clearFastPathEnv(t)
	t.Setenv("GEMINI_API_KEY", "test-gemini-key-12345")

	cfg := Load()
	if cfg.GeminiAPIKey != "test-gemini-key-12345" {
		t.Errorf("Expected GeminiAPIKey 'test-gemini-key-12345', got %q", cfg.GeminiAPIKey)
	}

	// Verify empty when not set
	clearFastPathEnv(t)
	cfg2 := Load()
	if cfg2.GeminiAPIKey != "" {
		t.Errorf("Expected empty GeminiAPIKey when not set, got %q", cfg2.GeminiAPIKey)
	}
}

func TestFastPathInvalidRateLimitFallback(t *testing.T) {
	clearFastPathEnv(t)
	t.Setenv("EMSC_FASTPATH_RATE_LIMIT_SEC", "0")

	cfg := Load()
	if cfg.EMSCFastPathRateLimitSec != 10 {
		t.Errorf("Expected invalid rate limit to fall back to 10, got %d", cfg.EMSCFastPathRateLimitSec)
	}
}
