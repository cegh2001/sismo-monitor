package llm

import (
	"testing"
	"time"

	"sismo-monitor/internal/alert"
)

func TestBuildPrompt(t *testing.T) {
	req := SynthesisRequest{
		TriggerType:    "INESTABILIDAD_CORTICAL",
		FaultName:      "Falla de El Pilar",
		CellID:         "G_22_12",
		BValue:         0.78,
		WeightedEnergy: 1.45e12,
		DynamicRate:    3.2,
		Mainshock: alert.Sismo{
			Magnitude: 4.8,
			Depth:     8.7,
			Location:  "Near Coast of Venezuela",
			Time:      time.Date(2026, 7, 19, 4, 54, 0, 0, time.UTC),
		},
		Phase: alert.PhasePrecursor,
	}

	prompt, err := BuildPrompt(req)
	if err != nil {
		t.Fatalf("BuildPrompt failed: %v", err)
	}

	if len(prompt) == 0 {
		t.Fatal("Expected non-empty prompt")
	}

	if !containsString(prompt, "Falla de El Pilar") {
		t.Errorf("Expected prompt to contain fault name, got:\n%s", prompt)
	}

	if !containsString(prompt, "INESTABILIDAD_CORTICAL") {
		t.Errorf("Expected prompt to contain trigger type, got:\n%s", prompt)
	}
}

func TestRateLimitLogic(t *testing.T) {
	synth := NewGemmaSynthesizer("dummy-key", nil)
	now := time.Now()

	// First attempt should pass check (isManual = false)
	if err := synth.checkRateLimit("G_1_1", false, now); err != nil {
		t.Fatalf("Expected no error on first check, got: %v", err)
	}

	// Record success
	synth.updateRateLimit("G_1_1", now)

	// Second immediate check for same cell must fail due to cell cooldown when isManual = false
	if err := synth.checkRateLimit("G_1_1", false, now.Add(1*time.Second)); err == nil {
		t.Errorf("Expected error on immediate repeat for same cell, got nil")
	}

	// Manual request (isManual = true) for same cell must BYPASS cell cooldown (after 11s global cooldown)
	if err := synth.checkRateLimit("G_1_1", true, now.Add(11*time.Second)); err != nil {
		t.Errorf("Expected manual trigger to bypass cell cooldown, got: %v", err)
	}

	// Check for a different cell immediately must fail due to global cooldown (10s)
	if err := synth.checkRateLimit("G_2_2", false, now.Add(1*time.Second)); err == nil {
		t.Errorf("Expected error on global cooldown, got nil")
	}

	// Check after global cooldown passes (11s) for new cell must succeed
	if err := synth.checkRateLimit("G_2_2", false, now.Add(11*time.Second)); err != nil {
		t.Errorf("Expected success for different cell after global cooldown, got: %v", err)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > len(sub) && findString(s, sub)))
}

func findString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
