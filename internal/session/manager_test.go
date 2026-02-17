package session_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/session"
	"github.com/Ask149/aidaemon/internal/testutil"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	response string
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", Name: "Mock Model"}}
}
func (m *mockProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: m.response}, nil
}
func (m *mockProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Delta: m.response, Done: true}
	close(ch)
	return ch, nil
}

func newTestManager(t *testing.T, response string) (*session.Manager, *testutil.MemoryStore) {
	t.Helper()
	st := testutil.NewMemoryStore(100)
	prov := &mockProvider{response: response}
	eng := &engine.Engine{Provider: prov}
	mgr := session.NewManager(session.ManagerConfig{
		Store:      st,
		Engine:     eng,
		Model:      "mock-model",
		TokenLimit: 128000,
		Threshold:  0.8,
	})
	return mgr, st
}

func TestHandleMessage_CreatesSession(t *testing.T) {
	mgr, st := newTestManager(t, "Hello back!")
	ctx := context.Background()

	result, err := mgr.HandleMessage(ctx, "ws-test", "Hello", session.HandleOptions{})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if result.Content != "Hello back!" {
		t.Errorf("expected 'Hello back!', got %q", result.Content)
	}

	// Session should have been created.
	sess, _ := st.ActiveSession("ws-test")
	if sess == nil {
		t.Fatal("expected active session")
	}
	if sess.Channel != "ws-test" || sess.Status != "active" {
		t.Errorf("unexpected session: %+v", sess)
	}
}

func TestHandleMessage_ReusesSession(t *testing.T) {
	mgr, st := newTestManager(t, "Response")
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "msg1", session.HandleOptions{})
	mgr.HandleMessage(ctx, "ws-test", "msg2", session.HandleOptions{})

	sessions, _ := st.ListAllSessions("ws-test")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
}

func TestActiveSession_ReturnsNilForNew(t *testing.T) {
	mgr, _ := newTestManager(t, "")
	sess, err := mgr.ActiveSession("nonexistent")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sess != nil {
		t.Fatalf("expected nil, got %+v", sess)
	}
}

func TestRotateSession_ClosesOldCreatesNew(t *testing.T) {
	mgr, st := newTestManager(t, "Response")
	ctx := context.Background()

	// Create initial session
	mgr.HandleMessage(ctx, "ws-test", "hello", session.HandleOptions{})
	oldSess, _ := st.ActiveSession("ws-test")
	oldID := oldSess.ID

	// Rotate
	newID, err := mgr.RotateSession(ctx, "ws-test")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newID == oldID {
		t.Error("expected new session ID")
	}

	// Old session should be closed
	old, _ := st.GetSession(oldID)
	if old.Status != "closed" {
		t.Errorf("expected closed, got %s", old.Status)
	}

	// New session should be active
	newSess, _ := st.GetSession(newID)
	if newSess.Status != "active" {
		t.Errorf("expected active, got %s", newSess.Status)
	}
}

func TestRenameSession(t *testing.T) {
	mgr, st := newTestManager(t, "ok")
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "hello", session.HandleOptions{})
	sess, _ := st.ActiveSession("ws-test")

	if err := mgr.RenameSession(sess.ID, "My Custom Title"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, _ := st.GetSession(sess.ID)
	if got.Title != "My Custom Title" {
		t.Errorf("expected 'My Custom Title', got %q", got.Title)
	}
}

func TestHandleMessage_RotatesOnThreshold(t *testing.T) {
	ctx := context.Background()

	// Use very low token limit to trigger rotation
	st := testutil.NewMemoryStore(100)
	prov := &mockProvider{response: "ok"}
	eng := &engine.Engine{Provider: prov}
	mgr := session.NewManager(session.ManagerConfig{
		Store:      st,
		Engine:     eng,
		Model:      "mock-model",
		TokenLimit: 100,
		Threshold:  0.8, // 80 tokens
	})

	// First message
	mgr.HandleMessage(ctx, "ws-test", "hello", session.HandleOptions{})
	sess1, _ := st.ActiveSession("ws-test")
	id1 := sess1.ID

	// Send big message that pushes over threshold
	bigMsg := strings.Repeat("word ", 200) // ~1000 chars = ~250 tokens > 80 threshold
	mgr.HandleMessage(ctx, "ws-test", bigMsg, session.HandleOptions{})

	// Should have rotated to new session
	sess2, _ := st.ActiveSession("ws-test")
	if sess2.ID == id1 {
		t.Error("expected rotation to new session")
	}
}
