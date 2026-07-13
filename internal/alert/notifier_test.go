package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPushoverNotifierMock(t *testing.T) {
	var receivedToken, receivedUser, receivedTitle, receivedMessage string

	// Create test HTTP server acting as the Pushover API
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		receivedToken = r.FormValue("token")
		receivedUser = r.FormValue("user")
		receivedTitle = r.FormValue("title")
		receivedMessage = r.FormValue("message")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-123"}`))
	}))
	defer ts.Close()

	// Initialize notifier with tokens and override the API URL to point to our test server
	notifier := NewPushoverNotifier("app-token-123", "user-key-456", nil)
	notifier.SetAPIURL(ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the notifier loop
	go notifier.Start(ctx)

	// Queue an alert
	alertEvent := Alert{
		Sismo: Sismo{
			ID:        "test-sim",
			Source:    "Simulation",
			Magnitude: 6.5,
			Depth:     10.0,
			Distance:  12.3,
			Location:  "La Guaira Port (Simulation)",
			Time:      time.Now(),
		},
		Level: LevelCritical,
	}

	if err := notifier.Notify(ctx, alertEvent); err != nil {
		t.Fatalf("Failed to queue alert: %v", err)
	}

	// Wait up to 2 seconds for notifier loop to read and send request
	limit := time.Now().Add(2 * time.Second)
	for time.Now().Before(limit) {
		if receivedToken != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if receivedToken != "app-token-123" {
		t.Errorf("Expected token 'app-token-123', got %q", receivedToken)
	}
	if receivedUser != "user-key-456" {
		t.Errorf("Expected user 'user-key-456', got %q", receivedUser)
	}
	if receivedTitle == "" || receivedMessage == "" {
		t.Errorf("Expected title and message to be formatted, got title: %q, message: %q", receivedTitle, receivedMessage)
	}
}

func TestPushoverNotifierLevelInstability(t *testing.T) {
	var receivedPriority, receivedRetry, receivedExpire string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		receivedPriority = r.FormValue("priority")
		receivedRetry = r.FormValue("retry")
		receivedExpire = r.FormValue("expire")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-123"}`))
	}))
	defer ts.Close()

	notifier := NewPushoverNotifier("app-token-123", "user-key-456", nil)
	notifier.SetAPIURL(ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go notifier.Start(ctx)

	alertEvent := Alert{
		Sismo: Sismo{
			ID:        "test-sim-inst",
			Source:    "Simulation",
			Magnitude: 2.5,
			Depth:     10.0,
			Distance:  12.3,
			Location:  "La Guaira Port (Simulation)",
			Time:      time.Now(),
			GridCell:  "G_0_0",
		},
		Level: LevelInstability,
	}

	if err := notifier.Notify(ctx, alertEvent); err != nil {
		t.Fatalf("Failed to queue alert: %v", err)
	}

	limit := time.Now().Add(2 * time.Second)
	for time.Now().Before(limit) {
		if receivedPriority != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if receivedPriority != "2" {
		t.Errorf("Expected priority '2', got %q", receivedPriority)
	}
	if receivedRetry != "30" {
		t.Errorf("Expected retry '30', got %q", receivedRetry)
	}
	if receivedExpire != "3600" {
		t.Errorf("Expected expire '3600', got %q", receivedExpire)
	}
}

func TestPushoverNotifierRateLimitSpacing(t *testing.T) {
	var sendTimes []time.Time

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendTimes = append(sendTimes, time.Now())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-123"}`))
	}))
	defer ts.Close()

	// Initialize notifier with 200ms rateLimitInterval
	notifier := NewPushoverNotifier("app-token-123", "user-key-456", nil)
	notifier.SetAPIURL(ts.URL)
	notifier.rateLimitInterval = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go notifier.Start(ctx)

	alertEvent := Alert{
		Sismo: Sismo{
			ID:        "test-sim-1",
			Magnitude: 4.5,
			Location:  "Caracas",
			Time:      time.Now(),
		},
		Level: LevelCritical,
	}

	// First alert
	if err := notifier.Notify(ctx, alertEvent); err != nil {
		t.Fatalf("Failed to queue alert 1: %v", err)
	}

	// Wait 250ms (longer than the 200ms rate limit interval) so that if there were a ticker,
	// it would tick, creating a stale tick in the channel buffer under the old implementation.
	time.Sleep(250 * time.Millisecond)

	// Now queue two alerts in rapid succession.
	// The first alert (alert 2) should be processed immediately because no alerts were sent in the last 250ms.
	// The second alert (alert 3) MUST be delayed by at least 200ms relative to alert 2.
	alertEvent2 := alertEvent
	alertEvent2.Sismo.ID = "test-sim-2"
	if err := notifier.Notify(ctx, alertEvent2); err != nil {
		t.Fatalf("Failed to queue alert 2: %v", err)
	}

	alertEvent3 := alertEvent
	alertEvent3.Sismo.ID = "test-sim-3"
	if err := notifier.Notify(ctx, alertEvent3); err != nil {
		t.Fatalf("Failed to queue alert 3: %v", err)
	}

	// We expect 3 alerts total. Wait up to 1.5 seconds for them to complete.
	limit := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(limit) {
		if len(sendTimes) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(sendTimes) < 3 {
		t.Fatalf("Expected 3 alerts to be sent, but only got %d", len(sendTimes))
	}

	// The delta between alert 2 (index 1) and alert 3 (index 2) must be at least 200ms.
	// We allow a small tolerance, e.g. 190ms, to account for system scheduler jitter.
	delta := sendTimes[2].Sub(sendTimes[1])
	if delta < 190*time.Millisecond {
		t.Errorf("Rate limit burst leak detected: time delta between back-to-back alerts was %v, expected >= 200ms", delta)
	}
}

func TestPushoverNotifierSendNowBypassesQueue(t *testing.T) {
	var sendCount int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-123"}`))
	}))
	defer ts.Close()

	notifier := NewPushoverNotifier("app-token-123", "user-key-456", nil)
	notifier.SetAPIURL(ts.URL)
	// High rate limit to ensure SendNow is NOT rate-limited
	notifier.rateLimitInterval = 10 * time.Second

	// Pre-populate queue with another alert that should NOT be sent (proving
	// SendNow is independent of the queue).
	pre := Alert{
		Sismo: Sismo{ID: "queued-pre", Magnitude: 4.0, Location: "pre", Time: time.Now()},
		Level: LevelInfo,
	}
	if err := notifier.Notify(context.Background(), pre); err != nil {
		t.Fatalf("Failed to queue pre-alert: %v", err)
	}

	// SendNow: must call send() synchronously, bypassing the queue
	now := Alert{
		Sismo: Sismo{ID: "sendnow-1", Magnitude: 5.5, Location: "LaGuaira", Time: time.Now()},
		Level: LevelPreAlert,
	}
	if err := notifier.SendNow(now); err != nil {
		t.Fatalf("SendNow failed: %v", err)
	}

	// The SendNow call must have produced exactly 1 HTTP send.
	if sendCount != 1 {
		t.Errorf("Expected exactly 1 send from SendNow, got %d", sendCount)
	}

	// Now drain the queue with Start (we must not call Start before SendNow to
	// avoid the queued pre-alert being sent and inflating the count).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go notifier.Start(ctx)

	// Wait until either the queued alert is sent or 1s elapses.
	limit := time.Now().Add(1 * time.Second)
	for time.Now().Before(limit) {
		if sendCount >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sendCount < 2 {
		t.Errorf("Expected queued alert to dispatch after Start, total sends=%d", sendCount)
	}
}

func TestPushoverNotifierSendNowLogsEarlyWarningPrefix(t *testing.T) {
	var logged []string
	logger := func(format string, args ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-abc"}`))
	}))
	defer ts.Close()

	notifier := NewPushoverNotifier("app-token-123", "user-key-456", logger)
	notifier.SetAPIURL(ts.URL)

	now := Alert{
		Sismo: Sismo{ID: "sendnow-prefix", Magnitude: 5.0, Location: "LaGuaira", Time: time.Now()},
		Level: LevelPreAlert,
	}
	if err := notifier.SendNow(now); err != nil {
		t.Fatalf("SendNow failed: %v", err)
	}

	// At least one log line must mention the alert's location.
	found := false
	for _, line := range logged {
		if strings.Contains(line, "LaGuaira") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected SendNow to log alert location; got logs: %v", logged)
	}
}

func TestPushoverNotifierRateLimitCancellation(t *testing.T) {
	var sendTimes []time.Time

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendTimes = append(sendTimes, time.Now())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1,"request":"req-123"}`))
	}))
	defer ts.Close()

	// Initialize notifier with 1 second rateLimitInterval
	notifier := NewPushoverNotifier("app-token-123", "user-key-456", nil)
	notifier.SetAPIURL(ts.URL)
	notifier.rateLimitInterval = 1 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	// Use manual cancel below
	_ = cancel

	go notifier.Start(ctx)

	alertEvent := Alert{
		Sismo: Sismo{
			ID:        "test-sim-1",
			Magnitude: 4.5,
			Location:  "Caracas",
			Time:      time.Now(),
		},
		Level: LevelCritical,
	}

	// First alert should go through immediately
	if err := notifier.Notify(ctx, alertEvent); err != nil {
		t.Fatalf("Failed to queue alert 1: %v", err)
	}

	// Wait a bit to ensure it is processed
	limit := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(limit) {
		if len(sendTimes) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(sendTimes) != 1 {
		t.Fatalf("Expected 1 alert to be sent immediately, got %d", len(sendTimes))
	}

	// Queue second alert. It should be rate-limited and sleep for 1 second.
	alertEvent2 := alertEvent
	alertEvent2.Sismo.ID = "test-sim-2"
	if err := notifier.Notify(ctx, alertEvent2); err != nil {
		t.Fatalf("Failed to queue alert 2: %v", err)
	}

	// Cancel context after 100ms, which is during the rate-limit sleep
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Wait 500ms and check that the second alert was never sent
	time.Sleep(500 * time.Millisecond)

	if len(sendTimes) != 1 {
		t.Errorf("Expected only 1 alert to be sent because context was cancelled, but got %d", len(sendTimes))
	}
}


