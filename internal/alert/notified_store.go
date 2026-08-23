package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// NotifiedStore persists the set of notification keys that have already been
// dispatched so that restarting the application does not re-fire alerts for
// events that were already notified in a previous session.
//
// The store is backed by a simple JSON file. Each entry records the key and
// the timestamp when it was first notified. Entries older than maxAge are
// pruned on Load and periodically via StartCleanup.
type NotifiedStore struct {
	mu       sync.Mutex
	path     string
	maxAge   time.Duration
	notified map[string]time.Time
}

// notifiedEntry is the on-disk JSON representation of a single notification record.
type notifiedEntry struct {
	Key        string    `json:"key"`
	NotifiedAt time.Time `json:"notified_at"`
}

// NewNotifiedStore creates a new NotifiedStore backed by the given file path.
// maxAge controls how long entries survive before being pruned (e.g. 30 days).
func NewNotifiedStore(path string, maxAge time.Duration) *NotifiedStore {
	return &NotifiedStore{
		path:     path,
		maxAge:   maxAge,
		notified: make(map[string]time.Time),
	}
}

// Load reads the persisted notification keys from disk. If the file does not
// exist, the store starts empty (no error). Expired entries are pruned
// immediately after loading.
func (s *NotifiedStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("notified store read: %w", err)
	}

	var entries []notifiedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("notified store unmarshal: %w", err)
	}

	cutoff := time.Now().Add(-s.maxAge)
	s.notified = make(map[string]time.Time, len(entries))
	for _, e := range entries {
		if e.NotifiedAt.After(cutoff) {
			s.notified[e.Key] = e.NotifiedAt
		}
	}
	return nil
}

// Save persists the current notification keys to disk atomically (write + rename).
func (s *NotifiedStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *NotifiedStore) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("notified store mkdir: %w", err)
	}

	entries := make([]notifiedEntry, 0, len(s.notified))
	for k, t := range s.notified {
		entries = append(entries, notifiedEntry{Key: k, NotifiedAt: t})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("notified store marshal: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("notified store write: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("notified store rename: %w", err)
	}
	return nil
}

// AlreadyNotified returns true if the given key has already been recorded.
func (s *NotifiedStore) AlreadyNotified(key string) bool {
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.notified[key]
	return ok
}

// MarkNotified records the key and persists the store to disk. If the key is
// empty, this is a no-op (defensive: callers with synthetic payloads that
// lack a meaningful ID should not poison the store).
func (s *NotifiedStore) MarkNotified(key string) error {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	s.notified[key] = time.Now()
	s.mu.Unlock()
	return s.Save()
}

// StartCleanup runs a background goroutine that periodically prunes expired
// entries and saves. It exits when ctx is cancelled.
func (s *NotifiedStore) StartCleanup(ctx interface{ Done() <-chan struct{} }) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.prune()
		}
	}
}

func (s *NotifiedStore) prune() {
	s.mu.Lock()
	cutoff := time.Now().Add(-s.maxAge)
	for k, t := range s.notified {
		if !t.After(cutoff) {
			delete(s.notified, k)
		}
	}
	s.mu.Unlock()
	// Best-effort persist after pruning; ignore error (logged by caller if needed).
	_ = s.Save()
}

// Len returns the number of entries currently in the store (for testing).
func (s *NotifiedStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.notified)
}

// NotificationKey builds a stable notification key for a seismic event.
// This is the primary key used to deduplicate event-level notifications.
func NotificationKey(sismoID string) string {
	if sismoID == "" {
		return ""
	}
	return "event:" + sismoID
}

// PhaseNotificationKey builds a stable notification key for a phase transition.
// Uses cell + phase + mainshock ID (or timestamp fallback) to uniquely identify
// the transition without colliding across re-entries.
func PhaseNotificationKey(cell string, phase SwarmPhase, mainshockID string, fallbackTime time.Time) string {
	id := mainshockID
	if id == "" {
		id = fmt.Sprintf("%d", fallbackTime.Unix())
	}
	return fmt.Sprintf("phase:%s:%d:%s", cell, int(phase), id)
}

// SwarmNotificationKey builds a stable notification key for a swarm alert.
func SwarmNotificationKey(triggerEventID string) string {
	if triggerEventID == "" {
		return ""
	}
	return "swarm:" + triggerEventID
}
