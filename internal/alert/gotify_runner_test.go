package alert

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsGotifyReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"health":"green"}`))
	}))
	defer server.Close()

	if !isGotifyReachable(server.URL) {
		t.Errorf("Expected server at %s to be reachable", server.URL)
	}

	if isGotifyReachable("http://127.0.0.1:59999") {
		t.Errorf("Expected non-existent port to be unreachable")
	}
}

func TestEnsureGotifyServerRunningAlreadyReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var logged []string
	logger := func(format string, args ...interface{}) {
		logged = append(logged, format)
	}

	err := EnsureGotifyServerRunning(context.Background(), server.URL, logger)
	if err != nil {
		t.Fatalf("Unexpected error when server is already reachable: %v", err)
	}

	if len(logged) == 0 {
		t.Errorf("Expected logger output")
	}
}
