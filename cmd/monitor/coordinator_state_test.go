package main

import (
	"path/filepath"
	"sismo-monitor/internal/alert"
	"testing"
	"time"
)

func TestCoordinatorStateStore_Empty(t *testing.T) {
	dir := t.TempDir()
	store := NewCoordinatorStateStore(filepath.Join(dir, "state.json"))

	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Should return zero value for unknown cell
	if got := store.GetPrevState("G_22_12"); got != alert.PhaseEstable {
		t.Errorf("GetPrevState = %v, want PhaseEstable", got)
	}

	// Should return false for unknown cooldown
	if _, ok := store.GetCooldown("G_22_12"); ok {
		t.Error("GetCooldown should return false for unknown cell")
	}

	// Should return false for unknown notification
	if store.AlreadyNotified("event:test") {
		t.Error("AlreadyNotified should return false for unknown key")
	}
}

func TestCoordinatorStateStore_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	store := NewCoordinatorStateStore(filepath.Join(dir, "state.json"))

	// Set prevState
	if err := store.SetPrevState("G_22_12", alert.PhasePrecursor); err != nil {
		t.Fatalf("SetPrevState: %v", err)
	}
	if got := store.GetPrevState("G_22_12"); got != alert.PhasePrecursor {
		t.Errorf("GetPrevState = %v, want PhasePrecursor", got)
	}

	// Set cooldown
	now := time.Now()
	if err := store.SetCooldown("G_22_12", now); err != nil {
		t.Fatalf("SetCooldown: %v", err)
	}
	if got, ok := store.GetCooldown("G_22_12"); !ok || !got.Equal(now) {
		t.Errorf("GetCooldown = %v, %v, want %v, true", got, ok, now)
	}

	// Mark notified
	if err := store.MarkNotified("event:test"); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	if !store.AlreadyNotified("event:test") {
		t.Error("AlreadyNotified should return true after marking")
	}
}

func TestCoordinatorStateStore_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Create store and set some state
	store1 := NewCoordinatorStateStore(path)
	store1.SetPrevState("G_22_12", alert.PhaseReplicas)
	store1.SetCooldown("G_22_12", time.Now())
	store1.MarkNotified("event:abc")

	// Create new store and load
	store2 := NewCoordinatorStateStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify state survived
	if got := store2.GetPrevState("G_22_12"); got != alert.PhaseReplicas {
		t.Errorf("GetPrevState after reload = %v, want PhaseReplicas", got)
	}
	if _, ok := store2.GetCooldown("G_22_12"); !ok {
		t.Error("GetCooldown after reload should return true")
	}
	if !store2.AlreadyNotified("event:abc") {
		t.Error("AlreadyNotified after reload should return true")
	}
}

func TestCoordinatorStateStore_PruneNotified(t *testing.T) {
	dir := t.TempDir()
	store := NewCoordinatorStateStore(filepath.Join(dir, "state.json"))

	// Mark some notifications
	store.MarkNotified("event:old")
	store.MarkNotified("event:new")

	// Manually set old timestamp (simulate old entry)
	state := store.GetAllState()
	state.Notified["event:old"] = time.Now().Add(-48 * time.Hour)
	// Save directly to bypass the lock
	store.mu.Lock()
	store.state = state
	store.mu.Unlock()
	store.Save()

	// Prune with 24h maxAge
	if err := store.PruneNotified(24 * time.Hour); err != nil {
		t.Fatalf("PruneNotified: %v", err)
	}

	// Old entry should be gone
	if store.AlreadyNotified("event:old") {
		t.Error("Old entry should have been pruned")
	}
	// New entry should survive
	if !store.AlreadyNotified("event:new") {
		t.Error("New entry should survive pruning")
	}
}

func TestCoordinatorStateStore_EmptyKeyIsNoop(t *testing.T) {
	dir := t.TempDir()
	store := NewCoordinatorStateStore(filepath.Join(dir, "state.json"))

	if store.AlreadyNotified("") {
		t.Error("Empty key should never be notified")
	}
	if err := store.MarkNotified(""); err != nil {
		t.Errorf("MarkNotified empty key should be no-op: %v", err)
	}
}
