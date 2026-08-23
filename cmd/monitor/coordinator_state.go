package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sismo-monitor/internal/alert"
	"sync"
	"time"
)

// CoordinatorState persists the coordinator's in-memory state across restarts.
// This prevents re-firing notifications and re-triggering LLM synthesis for
// events that were already processed in a previous session.
type CoordinatorState struct {
	// Per-cell previous phase (used for transition detection).
	PrevState map[string]alert.SwarmPhase `json:"prev_state"`
	// Per-cell last-notified time (cooldown).
	Cooldown map[string]time.Time `json:"cooldown"`
	// Notification keys already dispatched (survives restarts).
	Notified map[string]time.Time `json:"notified"`
}

// CoordinatorStateStore manages persistent coordinator state.
type CoordinatorStateStore struct {
	mu    sync.Mutex
	path  string
	state CoordinatorState
}

// NewCoordinatorStateStore creates a new state store backed by the given file path.
func NewCoordinatorStateStore(path string) *CoordinatorStateStore {
	return &CoordinatorStateStore{
		path: path,
		state: CoordinatorState{
			PrevState: make(map[string]alert.SwarmPhase),
			Cooldown:  make(map[string]time.Time),
			Notified:  make(map[string]time.Time),
		},
	}
}

// Load reads the coordinator state from disk. If the file does not exist,
// the state starts empty (no error).
func (s *CoordinatorStateStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("coordinator state read: %w", err)
	}

	var state CoordinatorState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("coordinator state unmarshal: %w", err)
	}

	// Initialize maps if they were nil in the JSON
	if state.PrevState == nil {
		state.PrevState = make(map[string]alert.SwarmPhase)
	}
	if state.Cooldown == nil {
		state.Cooldown = make(map[string]time.Time)
	}
	if state.Notified == nil {
		state.Notified = make(map[string]time.Time)
	}

	s.state = state
	return nil
}

// Save persists the current coordinator state to disk atomically.
func (s *CoordinatorStateStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *CoordinatorStateStore) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("coordinator state mkdir: %w", err)
	}

	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("coordinator state marshal: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("coordinator state write: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("coordinator state rename: %w", err)
	}
	return nil
}

// GetPrevState returns the previous phase for a cell.
func (s *CoordinatorStateStore) GetPrevState(cell string) alert.SwarmPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.PrevState[cell]
}

// SetPrevState sets the previous phase for a cell and persists.
func (s *CoordinatorStateStore) SetPrevState(cell string, phase alert.SwarmPhase) error {
	s.mu.Lock()
	s.state.PrevState[cell] = phase
	s.mu.Unlock()
	return s.Save()
}

// GetCooldown returns the last-notified time for a cell.
func (s *CoordinatorStateStore) GetCooldown(cell string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.state.Cooldown[cell]
	return t, ok
}

// SetCooldown sets the last-notified time for a cell and persists.
func (s *CoordinatorStateStore) SetCooldown(cell string, t time.Time) error {
	s.mu.Lock()
	s.state.Cooldown[cell] = t
	s.mu.Unlock()
	return s.Save()
}

// AlreadyNotified returns true if the notification key was already dispatched.
func (s *CoordinatorStateStore) AlreadyNotified(key string) bool {
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.state.Notified[key]
	return ok
}

// MarkNotified records the notification key and persists.
func (s *CoordinatorStateStore) MarkNotified(key string) error {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	s.state.Notified[key] = time.Now()
	s.mu.Unlock()
	return s.Save()
}

// PruneNotified removes notification keys older than maxAge.
func (s *CoordinatorStateStore) PruneNotified(maxAge time.Duration) error {
	s.mu.Lock()
	cutoff := time.Now().Add(-maxAge)
	for k, t := range s.state.Notified {
		if !t.After(cutoff) {
			delete(s.state.Notified, k)
		}
	}
	s.mu.Unlock()
	return s.Save()
}

// GetAllState returns the full state (for debugging/testing).
func (s *CoordinatorStateStore) GetAllState() CoordinatorState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}
