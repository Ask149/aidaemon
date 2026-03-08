package heartbeat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNotified_CheckAndMark(t *testing.T) {
	dir := t.TempDir()
	n := NewNotifiedTracker(filepath.Join(dir, "notified.jsonl"))

	// First time — not notified.
	if n.AlreadyNotified("calendar", "event_123") {
		t.Error("should not be notified yet")
	}

	// Mark it.
	if err := n.MarkNotified("calendar", "event_123"); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// Now it should be notified.
	if !n.AlreadyNotified("calendar", "event_123") {
		t.Error("should be notified after marking")
	}

	// Different signal — not notified.
	if n.AlreadyNotified("email", "msg_456") {
		t.Error("different signal should not be notified")
	}
}

func TestNotified_Prune(t *testing.T) {
	dir := t.TempDir()
	n := NewNotifiedTracker(filepath.Join(dir, "notified.jsonl"))

	n.MarkNotified("calendar", "old_event")

	// Prune everything older than 0 seconds (everything).
	if err := n.PruneOlderThan(0 * time.Second); err != nil {
		t.Fatalf("prune: %v", err)
	}

	if n.AlreadyNotified("calendar", "old_event") {
		t.Error("should be pruned")
	}
}
