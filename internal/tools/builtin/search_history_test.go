package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Ask149/aidaemon/internal/store"
)

func TestSearchHistoryTool_Execute(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()

	st.AddMessage("telegram:s_abc123", "user", "My AWS account ID is 123456789012")
	st.AddMessage("telegram:s_def456", "user", "Let's discuss Kubernetes")

	tool := &SearchHistoryTool{Store: st}

	if tool.Name() != "search_history" {
		t.Errorf("name = %s, want search_history", tool.Name())
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "AWS",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 result, got %d", len(parsed))
	}
}

func TestSearchHistoryTool_MissingQuery(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()

	tool := &SearchHistoryTool{Store: st}
	_, err = tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestSearchHistoryTool_WithLimit(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()

	for i := 0; i < 20; i++ {
		st.AddMessage("telegram:s_abc123", "user", "searchterm in message")
	}

	tool := &SearchHistoryTool{Store: st}
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "searchterm",
		"limit": float64(3), // JSON numbers are float64
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var parsed []map[string]interface{}
	json.Unmarshal([]byte(result), &parsed)
	if len(parsed) != 3 {
		t.Fatalf("expected 3 results, got %d", len(parsed))
	}
}
