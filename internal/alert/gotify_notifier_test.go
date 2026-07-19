package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGotifyNotifierMock(t *testing.T) {
	var logged []string
	logger := func(format string, args ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}

	notifier := NewGotifyNotifier("", "", logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go notifier.Start(ctx)

	// Non-simulated event -> should NOT log mock alert
	realEvent := Alert{
		Level: LevelCritical,
		Sismo: Sismo{
			ID:        "real-123",
			Source:    "USGS",
			Magnitude: 5.0,
			Location:  "Caracas",
		},
	}
	_ = notifier.SendNow(realEvent)

	// Simulated event -> SHOULD log mock alert
	simEvent := Alert{
		Level: LevelInstability,
		Sismo: Sismo{
			ID:        "sim-456",
			Source:    "Simulation",
			Magnitude: 6.2,
			Location:  "Valencia",
			Distance:  120.0,
		},
	}
	_ = notifier.SendNow(simEvent)

	// Verify only simulated event logged mock alert
	mockCount := 0
	for _, l := range logged {
		if strings.Contains(l, "[MOCK GOTIFY ALERT]") {
			mockCount++
			if !strings.Contains(l, "Valencia") {
				t.Errorf("Expected log to be for simulated event Valencia, got: %s", l)
			}
		}
	}
	if mockCount != 1 {
		t.Errorf("Expected 1 mock alert log for simulated event, got %d", mockCount)
	}
}

func TestGotifyNotifierSend(t *testing.T) {
	var receivedPayload gotifyMessagePayload
	var receivedToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message" {
			t.Errorf("Expected path /message, got %s", r.URL.Path)
		}
		receivedToken = r.Header.Get("X-Gotify-Key")
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()

	notifier := NewGotifyNotifier(server.URL, "test-token-123", nil)

	alert := Alert{
		Level: LevelCritical,
		Sismo: Sismo{
			ID:        "evt-789",
			Source:    "Funvisis",
			Magnitude: 4.8,
			Location:  "Near Coast",
			Distance:  50.0,
			Time:      time.Now(),
		},
	}

	err := notifier.SendNow(alert)
	if err != nil {
		t.Fatalf("Unexpected error sending Gotify alert: %v", err)
	}

	if receivedToken != "test-token-123" {
		t.Errorf("Expected token test-token-123, got %s", receivedToken)
	}
	if receivedPayload.Priority != 8 {
		t.Errorf("Expected priority 8 for LevelCritical, got %d", receivedPayload.Priority)
	}
	if !strings.Contains(receivedPayload.Title, "Seismic Alert") {
		t.Errorf("Expected title to contain 'Seismic Alert', got %s", receivedPayload.Title)
	}
}

func TestGotifyNotifierSendSynthesisReport(t *testing.T) {
	var receivedPayload gotifyMessagePayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":2}`))
	}))
	defer server.Close()

	notifier := NewGotifyNotifier(server.URL, "test-token", nil)

	report := SynthesisResponse{
		ReportType: ReportConfirmacion,
		Summary:    "Actividad confirmada en falla de San Sebastián",
		Body:       "Se confirma enjambre sísmico en el segmento costero.",
		ModelUsed:  "gemma-2b",
		Citations: []Citation{
			{Title: "Funvisis Report", URL: "https://funvisis.gob.ve/123"},
		},
	}

	err := notifier.SendSynthesisReport(report)
	if err != nil {
		t.Fatalf("Unexpected error sending synthesis report: %v", err)
	}

	if receivedPayload.Priority != 8 {
		t.Errorf("Expected priority 8 for confirmation report, got %d", receivedPayload.Priority)
	}
	if !strings.Contains(receivedPayload.Message, "Funvisis Report") {
		t.Errorf("Expected markdown citations in message body, got %s", receivedPayload.Message)
	}
}
