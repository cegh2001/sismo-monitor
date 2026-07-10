package alert

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	notifier.apiURL = ts.URL

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
