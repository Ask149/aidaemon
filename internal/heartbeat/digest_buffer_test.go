package heartbeat

import (
	"path/filepath"
	"testing"
)

func TestDigestBuffer_AppendAndRead(t *testing.T) {
	dir := t.TempDir()
	buf := NewDigestBuffer(filepath.Join(dir, "digest-buffer.jsonl"))

	err := buf.Append(DigestEntry{
		Type:    "awareness",
		Urgency: "routine",
		Signal:  "email",
		Message: "Email from recruiter — unanswered",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	err = buf.Append(DigestEntry{
		Type:    "news",
		Topic:   "AI & LLMs",
		Message: "OpenAI releases GPT-5",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	entries, err := buf.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Signal != "email" {
		t.Errorf("entries[0].Signal = %q, want email", entries[0].Signal)
	}
	if entries[1].Topic != "AI & LLMs" {
		t.Errorf("entries[1].Topic = %q, want AI & LLMs", entries[1].Topic)
	}
}

func TestDigestBuffer_Clear(t *testing.T) {
	dir := t.TempDir()
	buf := NewDigestBuffer(filepath.Join(dir, "digest-buffer.jsonl"))

	buf.Append(DigestEntry{Type: "test", Message: "hello"})
	if err := buf.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	entries, err := buf.Read()
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries after clear, want 0", len(entries))
	}
}

func TestDigestBuffer_ReadEmpty(t *testing.T) {
	dir := t.TempDir()
	buf := NewDigestBuffer(filepath.Join(dir, "nonexistent.jsonl"))

	entries, err := buf.Read()
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}
