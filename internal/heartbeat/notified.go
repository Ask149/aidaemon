package heartbeat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// NotifiedEntry tracks a signal that has been sent.
type NotifiedEntry struct {
	Signal     string    `json:"signal"`
	ID         string    `json:"id"`
	NotifiedAt time.Time `json:"notified_at"`
}

// NotifiedTracker manages deduplication of notifications.
type NotifiedTracker struct {
	path string
}

// NewNotifiedTracker creates a tracker at the given file path.
func NewNotifiedTracker(path string) *NotifiedTracker {
	return &NotifiedTracker{path: path}
}

// AlreadyNotified checks if a signal+id combination has been sent.
func (n *NotifiedTracker) AlreadyNotified(signal, id string) bool {
	entries := n.readAll()
	for _, e := range entries {
		if e.Signal == signal && e.ID == id {
			return true
		}
	}
	return false
}

// MarkNotified records that a signal+id has been sent.
func (n *NotifiedTracker) MarkNotified(signal, id string) error {
	if err := os.MkdirAll(filepath.Dir(n.path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	f, err := os.OpenFile(n.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open notified: %w", err)
	}
	defer f.Close()

	entry := NotifiedEntry{
		Signal:     signal,
		ID:         id,
		NotifiedAt: time.Now(),
	}
	data, _ := json.Marshal(entry)
	_, err = f.Write(append(data, '\n'))
	return err
}

// PruneOlderThan removes entries older than maxAge.
func (n *NotifiedTracker) PruneOlderThan(maxAge time.Duration) error {
	entries := n.readAll()
	cutoff := time.Now().Add(-maxAge)

	var kept []NotifiedEntry
	for _, e := range entries {
		if e.NotifiedAt.After(cutoff) {
			kept = append(kept, e)
		}
	}

	// Rewrite file with only kept entries.
	f, err := os.Create(n.path)
	if err != nil {
		return fmt.Errorf("rewrite notified: %w", err)
	}
	defer f.Close()

	for _, e := range kept {
		data, _ := json.Marshal(e)
		f.Write(append(data, '\n'))
	}

	return nil
}

func (n *NotifiedTracker) readAll() []NotifiedEntry {
	f, err := os.Open(n.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []NotifiedEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e NotifiedEntry
		if json.Unmarshal(scanner.Bytes(), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries
}
