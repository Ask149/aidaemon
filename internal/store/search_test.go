package store

import (
	"path/filepath"
	"testing"
)

func TestSearch_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddMessage("telegram:s_abc123", "user", "My AWS account ID is 123456789012")
	s.AddMessage("telegram:s_abc123", "assistant", "Got it, I'll remember that.")
	s.AddMessage("telegram:s_def456", "user", "Let's discuss the Kubernetes migration")

	results, err := s.Search("AWS", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Role != "user" {
		t.Errorf("expected role=user, got %s", results[0].Role)
	}
	if results[0].ChatID != "telegram:s_abc123" {
		t.Errorf("expected chat_id=telegram:s_abc123, got %s", results[0].ChatID)
	}
}

func TestSearch_NoResults(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddMessage("telegram:s_abc123", "user", "Hello world")

	results, err := s.Search("nonexistent", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_BackfillExistingData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Simulate a pre-FTS database: open store, drop FTS artifacts, insert
	// data directly (bypassing triggers), then close and reopen.
	s1, err := New(dbPath, 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	// Drop FTS table and triggers so data is inserted without FTS indexing.
	s1.db.Exec("DROP TRIGGER IF EXISTS conversations_ai")
	s1.db.Exec("DROP TRIGGER IF EXISTS conversations_ad")
	s1.db.Exec("DROP TRIGGER IF EXISTS conversations_au")
	s1.db.Exec("DROP TABLE IF EXISTS conversations_fts")

	// Insert directly — no FTS trigger fires.
	s1.db.Exec(`INSERT INTO conversations (chat_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		"telegram:s_abc123", "user", "pre-existing message about golang", 1000000)
	s1.Close()

	// Re-open — migrateFTS should detect empty FTS + non-empty conversations and backfill.
	s2, err := New(dbPath, 100)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s2.Close()

	results, err := s2.Search("golang", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after backfill, got %d", len(results))
	}
}

func TestSearch_Limit(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	for i := 0; i < 20; i++ {
		s.AddMessage("telegram:s_abc123", "user", "important keyword here")
	}

	results, err := s.Search("keyword", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
}

func TestSearch_DeletedMessageNotFound(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddMessage("telegram:s_abc123", "user", "searchable content here")
	s.ClearChat("telegram:s_abc123")

	results, err := s.Search("searchable", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results after clear, got %d", len(results))
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddMessage("telegram:s_abc123", "user", "some message")

	// Empty query should return empty results, not an error.
	results, err := s.Search("", 10)
	if err != nil {
		t.Fatalf("search empty: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty query, got %d", len(results))
	}

	// Whitespace-only query should also return empty results.
	results, err = s.Search("   ", 10)
	if err != nil {
		t.Fatalf("search whitespace: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for whitespace query, got %d", len(results))
	}
}
