package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"sismo-monitor/internal/alert"
)

func TestEMSCMessageMapping(t *testing.T) {
	client := NewEMSCClient(nil)

	// Mock raw JSON payload from EMSC WS
	jsonPayload := `{
		"action": "create",
		"data": {
			"type": "Feature",
			"properties": {
				"unid": "emsc-event-99",
				"time": "2026-07-10T16:20:00.0Z",
				"mag": 5.8,
				"depth": 25.0,
				"flynn_region": "CARIBBEAN SEA",
				"lat": 10.80,
				"lon": -66.50
			},
			"geometry": {
				"type": "Point",
				"coordinates": [-66.50, 10.80]
			}
		}
	}`

	var msg alertMessage
	if err := json.Unmarshal([]byte(jsonPayload), &msg); err != nil {
		t.Fatalf("Failed to parse mock message: %v", err)
	}

	sismo := client.mapMessageToSismo(msg)

	if sismo.ID != "emsc-event-99" {
		t.Errorf("Expected ID 'emsc-event-99', got %q", sismo.ID)
	}
	if sismo.Source != "EMSC" {
		t.Errorf("Expected Source 'EMSC', got %q", sismo.Source)
	}
	if sismo.Magnitude != 5.8 {
		t.Errorf("Expected Magnitude 5.8, got %.1f", sismo.Magnitude)
	}
	if sismo.Depth != 25.0 {
		t.Errorf("Expected Depth 25.0, got %.1f", sismo.Depth)
	}
	if sismo.Location != "CARIBBEAN SEA" {
		t.Errorf("Expected location 'CARIBBEAN SEA', got %q", sismo.Location)
	}
	if sismo.Distance <= 0.0 {
		t.Errorf("Expected distance calculated, got %.1f", sismo.Distance)
	}
}

func TestEMSCReconnection(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	connectionCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectionCount++
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Upgrade error: %v", err)
			return
		}
		// Immediately close connection to trigger client reconnect
		conn.Close()
	}))
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)

	client := NewEMSCClient(nil)
	client.url = wsURL
	client.reconnectDelay = 1 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := make(chan alert.Sismo, 10)

	// Run Start in background
	go client.Start(ctx, out)

	// Wait briefly to allow reconnection attempts
	time.Sleep(50 * time.Millisecond)
	cancel()

	if connectionCount < 2 {
		t.Errorf("Expected at least 2 connection attempts, got %d", connectionCount)
	}
}
