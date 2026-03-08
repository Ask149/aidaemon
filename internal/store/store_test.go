package store

import (
	"path/filepath"
	"testing"
)

// newTestStore creates a temporary SQLite store for testing.
func newTestStore(t *testing.T, limit int) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), limit)
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewAndClose(t *testing.T) {
	s := newTestStore(t, 100)
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestAddMessageAndGetHistory(t *testing.T) {
	s := newTestStore(t, 100)

	if err := s.AddMessage("chat1", "user", "hello"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.AddMessage("chat1", "assistant", "hi there"); err != nil {
		t.Fatalf("add: %v", err)
	}

	msgs, err := s.GetHistory("chat1")
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Should be oldest→newest.
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("msg[0] = %+v, want user/hello", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("msg[1] = %+v, want assistant/hi there", msgs[1])
	}
}

func TestGetHistoryEmpty(t *testing.T) {
	s := newTestStore(t, 100)

	msgs, err := s.GetHistory("nonexistent")
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestGetHistoryIsolation(t *testing.T) {
	s := newTestStore(t, 100)

	s.AddMessage("chat1", "user", "msg1")
	s.AddMessage("chat2", "user", "msg2")

	msgs1, _ := s.GetHistory("chat1")
	msgs2, _ := s.GetHistory("chat2")

	if len(msgs1) != 1 || msgs1[0].Content != "msg1" {
		t.Errorf("chat1 leaked: %+v", msgs1)
	}
	if len(msgs2) != 1 || msgs2[0].Content != "msg2" {
		t.Errorf("chat2 leaked: %+v", msgs2)
	}
}

func TestMessageCount(t *testing.T) {
	s := newTestStore(t, 100)

	count, _ := s.MessageCount("chat1")
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	s.AddMessage("chat1", "user", "a")
	s.AddMessage("chat1", "user", "b")

	count, _ = s.MessageCount("chat1")
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}
}

func TestClearChat(t *testing.T) {
	s := newTestStore(t, 100)

	s.AddMessage("chat1", "user", "a")
	s.AddMessage("chat1", "user", "b")

	if err := s.ClearChat("chat1"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	count, _ := s.MessageCount("chat1")
	if count != 0 {
		t.Errorf("expected 0 after clear, got %d", count)
	}
}

func TestClearChatDoesntAffectOthers(t *testing.T) {
	s := newTestStore(t, 100)

	s.AddMessage("chat1", "user", "a")
	s.AddMessage("chat2", "user", "b")

	s.ClearChat("chat1")

	count, _ := s.MessageCount("chat2")
	if count != 1 {
		t.Errorf("clearing chat1 affected chat2: count=%d", count)
	}
}

func TestAutoTrim(t *testing.T) {
	s := newTestStore(t, 3) // limit of 3

	s.AddMessage("chat1", "user", "msg1")
	s.AddMessage("chat1", "user", "msg2")
	s.AddMessage("chat1", "user", "msg3")
	s.AddMessage("chat1", "user", "msg4") // should trim msg1

	count, _ := s.MessageCount("chat1")
	if count != 3 {
		t.Errorf("expected 3 after trim, got %d", count)
	}

	msgs, _ := s.GetHistory("chat1")
	if msgs[0].Content != "msg2" {
		t.Errorf("oldest should be msg2, got %q", msgs[0].Content)
	}
	if msgs[2].Content != "msg4" {
		t.Errorf("newest should be msg4, got %q", msgs[2].Content)
	}
}

func TestLimit(t *testing.T) {
	s := newTestStore(t, 42)
	if s.Limit() != 42 {
		t.Errorf("Limit() = %d, want 42", s.Limit())
	}
}

func TestGetOldestN(t *testing.T) {
	s := newTestStore(t, 100)

	s.AddMessage("chat1", "user", "first")
	s.AddMessage("chat1", "user", "second")
	s.AddMessage("chat1", "user", "third")

	oldest, err := s.GetOldestN("chat1", 2)
	if err != nil {
		t.Fatalf("GetOldestN: %v", err)
	}
	if len(oldest) != 2 {
		t.Fatalf("expected 2, got %d", len(oldest))
	}
	if oldest[0].Content != "first" {
		t.Errorf("oldest[0] = %q, want first", oldest[0].Content)
	}
	if oldest[1].Content != "second" {
		t.Errorf("oldest[1] = %q, want second", oldest[1].Content)
	}
	// IDs should be populated.
	if oldest[0].ID == 0 {
		t.Error("expected non-zero ID")
	}
}

func TestReplaceMessages(t *testing.T) {
	s := newTestStore(t, 100)

	s.AddMessage("chat1", "user", "old1")
	s.AddMessage("chat1", "user", "old2")
	s.AddMessage("chat1", "user", "keep")

	oldest, _ := s.GetOldestN("chat1", 2)
	ids := []int64{oldest[0].ID, oldest[1].ID}

	err := s.ReplaceMessages("chat1", ids, "system", "[Summary]")
	if err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}

	// Should have 2 messages: summary + "keep".
	count, _ := s.MessageCount("chat1")
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}

	msgs, _ := s.GetHistory("chat1")
	// Verify both messages exist (order may vary when timestamps collide in tests).
	contents := map[string]bool{}
	for _, m := range msgs {
		contents[m.Content] = true
	}
	if !contents["[Summary]"] {
		t.Error("missing [Summary] message")
	}
	if !contents["keep"] {
		t.Error("missing keep message")
	}
}

func TestReplaceMessagesEmpty(t *testing.T) {
	s := newTestStore(t, 100)

	// No-op with empty IDs.
	err := s.ReplaceMessages("chat1", nil, "system", "summary")
	if err != nil {
		t.Fatalf("ReplaceMessages with nil IDs: %v", err)
	}
}

func TestListSessions(t *testing.T) {
	st := newTestStore(t, 20)

	// Empty store.
	sessions, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}

	// Insert messages with explicit timestamps to guarantee ordering.
	// chat-1: two messages at t=1000 and t=1001
	// chat-2: one message at t=2000 (more recent)
	st.db.Exec("INSERT INTO conversations (chat_id, role, content, created_at) VALUES (?, ?, ?, ?)", "chat-1", "user", "hello", 1000)
	st.db.Exec("INSERT INTO conversations (chat_id, role, content, created_at) VALUES (?, ?, ?, ?)", "chat-1", "assistant", "hi", 1001)
	st.db.Exec("INSERT INTO conversations (chat_id, role, content, created_at) VALUES (?, ?, ?, ?)", "chat-2", "user", "yo", 2000)

	sessions, err = st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}

	// Check session info.
	sessionMap := make(map[string]SessionInfo)
	for _, s := range sessions {
		sessionMap[s.ChatID] = s
	}

	if s, ok := sessionMap["chat-1"]; !ok || s.MessageCount != 2 {
		t.Errorf("chat-1: expected 2 messages, got %+v", sessionMap["chat-1"])
	}
	if s, ok := sessionMap["chat-2"]; !ok || s.MessageCount != 1 {
		t.Errorf("chat-2: expected 1 message, got %+v", sessionMap["chat-2"])
	}

	// Verify ordering: most recently active first (chat-2 has t=2000, chat-1 has t=1001).
	if len(sessions) >= 2 && sessions[0].ChatID != "chat-2" {
		t.Errorf("expected most recent session first, got %s", sessions[0].ChatID)
	}
}

// TestConversationInterface verifies that *SQLiteStore implements Conversation.
func TestConversationInterface(t *testing.T) {
	var _ Conversation = newTestStore(t, 10)
}
