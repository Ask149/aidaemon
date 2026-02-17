package session_test

import (
	"context"
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
