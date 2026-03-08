package heartbeat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DigestEntry represents a single item in the digest buffer.
type DigestEntry struct {
	Type    string `json:"type"`              // "awareness", "news", "curiosity"
	Urgency string `json:"urgency,omitempty"` // "urgent" or "routine"
	Signal  string `json:"signal,omitempty"`  // e.g., "calendar", "email", "goal"
	Topic   string `json:"topic,omitempty"`   // news topic label
	Goal    string `json:"goal,omitempty"`    // goal ID for curiosity
	Message string `json:"message"`
}

// DigestBuffer manages an append-only JSONL buffer file.
type DigestBuffer struct {
	path string
}

// NewDigestBuffer creates a buffer at the given file path.
func NewDigestBuffer(path string) *DigestBuffer {
	return &DigestBuffer{path: path}
}

// Append adds an entry to the buffer.
func (b *DigestBuffer) Append(entry DigestEntry) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0755); err != nil {
		return fmt.Errorf("create buffer dir: %w", err)
	}

	f, err := os.OpenFile(b.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open buffer: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	return nil
}

// Read returns all entries in the buffer.
func (b *DigestBuffer) Read() ([]DigestEntry, error) {
	f, err := os.Open(b.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open buffer: %w", err)
	}
	defer f.Close()

	var entries []DigestEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry DigestEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("scan buffer: %w", err)
	}

	return entries, nil
}

// Clear truncates the buffer file.
func (b *DigestBuffer) Clear() error {
	return os.WriteFile(b.path, nil, 0644)
}
