package alert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifiedStore_AlreadyNotified_Empty(t *testing.T) {
	dir := t.TempDir()
	store := NewNotifiedStore(filepath.Join(dir, "notified.json"), 30*24*time.Hour)

	if store.AlreadyNotified("event:abc") {
		t.Fatal("expected false for empty store")
	}
}

func TestNotifiedStore_MarkAndCheck(t *testing.T) {
	dir := t.TempDir()
	store := NewNotifiedStore(filepath.Join(dir, "notified.json"), 30*24*time.Hour)

	if err := store.MarkNotified("event:abc"); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	if !store.AlreadyNotified("event:abc") {
		t.Fatal("expected true after marking")
	}
	if store.AlreadyNotified("event:xyz") {
		t.Fatal("expected false for unmarked key")
	}
}

func TestNotifiedStore_EmptyKeyIsNoop(t *testing.T) {
	dir := t.TempDir()
	store := NewNotifiedStore(filepath.Join(dir, "notified.json"), 30*24*time.Hour)

	if store.AlreadyNotified("") {
		t.Fatal("empty key should never be notified")
	}
	if err := store.MarkNotified(""); err != nil {
		t.Fatalf("MarkNotified empty key should be no-op: %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", store.Len())
	}
}

func TestNotifiedStore_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notified.json")

	// Create store, mark a key, save
	store1 := NewNotifiedStore(path, 30*24*time.Hour)
	if err := store1.MarkNotified("event:persist-test"); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}

	// Create a new store instance from the same file
	store2 := NewNotifiedStore(path, 30*24*time.Hour)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !store2.AlreadyNotified("event:persist-test") {
		t.Fatal("expected key to survive reload")
	}
	if store2.AlreadyNotified("event:nonexistent") {
		t.Fatal("unexpected key found after reload")
	}
}

func TestNotifiedStore_LoadPrunesExpired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notified.json")

	// Write a file with one fresh and one expired entry
	fresh := notifiedEntry{Key: "event:fresh", NotifiedAt: time.Now()}
	expired := notifiedEntry{Key: "event:old", NotifiedAt: time.Now().Add(-48 * time.Hour)}

	entries := []notifiedEntry{fresh, expired}
	jsonData, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, jsonData, 0644); err != nil {
		t.Fatal(err)
	}

	// Load with 24h maxAge — expired entry should be pruned
	store := NewNotifiedStore(path, 24*time.Hour)
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !store.AlreadyNotified("event:fresh") {
		t.Fatal("fresh entry should survive")
	}
	if store.AlreadyNotified("event:old") {
		t.Fatal("expired entry should have been pruned")
	}
}

func TestNotifiedStore_LoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := NewNotifiedStore(filepath.Join(dir, "does-not-exist.json"), 24*time.Hour)
	if err := store.Load(); err != nil {
		t.Fatalf("Load should not error on missing file: %v", err)
	}
	if store.Len() != 0 {
		t.Fatal("expected empty store")
	}
}

func TestNotifiedStore_StartCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notified.json")
	store := NewNotifiedStore(path, 1*time.Millisecond) // very short maxAge

	store.MarkNotified("event:will-expire")

	// Wait for entry to expire, then prune
	time.Sleep(5 * time.Millisecond)
	store.prune()

	if store.AlreadyNotified("event:will-expire") {
		t.Fatal("entry should have been pruned")
	}
}

func TestNotificationKey(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"usgs://abc123", "event:usgs://abc123"},
		{"", ""},
	}
	for _, tt := range tests {
		got := NotificationKey(tt.id)
		if got != tt.want {
			t.Errorf("NotificationKey(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestPhaseNotificationKey(t *testing.T) {
	key := PhaseNotificationKey("G_22_12", PhaseReplicas, "usgs://abc", time.Time{})
	want := "phase:G_22_12:4:usgs://abc"
	if key != want {
		t.Errorf("PhaseNotificationKey = %q, want %q", key, want)
	}

	// Fallback to timestamp when mainshockID is empty
	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	key2 := PhaseNotificationKey("G_22_12", PhaseAtencion, "", ts)
	want2 := "phase:G_22_12:2:1768478400"
	if key2 != want2 {
		t.Errorf("PhaseNotificationKey fallback = %q, want %q", key2, want2)
	}
}

func TestSwarmNotificationKey(t *testing.T) {
	if got := SwarmNotificationKey("ev1"); got != "swarm:ev1" {
		t.Errorf("SwarmNotificationKey = %q, want %q", got, "swarm:ev1")
	}
	if got := SwarmNotificationKey(""); got != "" {
		t.Errorf("SwarmNotificationKey empty = %q, want empty", got)
	}
}
