package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sismo-monitor/internal/alert"
)

func TestHandleTestAlert(t *testing.T) {
	t.Run("Valid POST Request", func(t *testing.T) {
		out := make(chan alert.Sismo, 10)
		server := NewSimulationServer("8080", out, nil)

		payload := TestAlertPayload{
			Magnitude: 5.5,
			Latitude:  10.60,
			Longitude: -66.93,
			Depth:     15.0,
			Location:  "La Guaira Test",
		}
		body, _ := json.Marshal(payload)

		req := httptest.NewRequest(http.MethodPost, "/test-alert", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		server.handleTestAlert(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status code 200, got %d", resp.StatusCode)
		}

		// Verify event was pushed to the channel
		select {
		case sismo := <-out:
			if sismo.Magnitude != 5.5 {
				t.Errorf("Expected magnitude 5.5, got %.1f", sismo.Magnitude)
			}
			if sismo.Location != "La Guaira Test" {
				t.Errorf("Expected location 'La Guaira Test', got %q", sismo.Location)
			}
			if sismo.Source != "Simulation" {
				t.Errorf("Expected source 'Simulation', got %q", sismo.Source)
			}
		default:
			t.Fatal("Expected sismo event in channel, but got none")
		}
	})

	t.Run("Method Not Allowed", func(t *testing.T) {
		out := make(chan alert.Sismo, 10)
		server := NewSimulationServer("8080", out, nil)

		req := httptest.NewRequest(http.MethodGet, "/test-alert", nil)
		w := httptest.NewRecorder()

		server.handleTestAlert(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Expected status code 405, got %d", resp.StatusCode)
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		out := make(chan alert.Sismo, 10)
		server := NewSimulationServer("8080", out, nil)

		req := httptest.NewRequest(http.MethodPost, "/test-alert", bytes.NewBufferString("{invalid-json"))
		w := httptest.NewRecorder()

		server.handleTestAlert(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status code 400, got %d", resp.StatusCode)
		}
	})

	t.Run("Invalid Magnitude", func(t *testing.T) {
		out := make(chan alert.Sismo, 10)
		server := NewSimulationServer("8080", out, nil)

		payload := TestAlertPayload{
			Magnitude: -1.0,
			Latitude:  10.60,
			Longitude: -66.93,
		}
		body, _ := json.Marshal(payload)

		req := httptest.NewRequest(http.MethodPost, "/test-alert", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		server.handleTestAlert(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status code 400, got %d", resp.StatusCode)
		}
	})
}
