// Package session_test contains tests for session lifecycle operations.
package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/session"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/testutil"
)

func TestTitleGeneration(t *testing.T) {
	// Mock provider returns "Debugging WebSocket" as title.
	mgr, st := newTestManager(t, "Debugging WebSocket")
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "Help me fix the WebSocket reconnect", session.HandleOptions{})

	// Poll with timeout instead of fixed sleep.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		sess, _ := st.ActiveSession("ws-test")
		if sess != nil && sess.Title != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	sess, _ := st.ActiveSession("ws-test")
	if sess == nil {
		t.Fatal("no session")
	}
	// Verify exact title value.
	if sess.Title != "Debugging WebSocket" {
		t.Errorf("expected 'Debugging WebSocket', got %q", sess.Title)
	}
}

// mockErrorProvider implements provider.Provider that returns errors for Chat.
type mockErrorProvider struct {
	response  string
	callCount int
}

func (m *mockErrorProvider) Name() string { return "mock-error" }
func (m *mockErrorProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", Name: "Mock Model"}}
}
func (m *mockErrorProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	m.callCount++
	// First call (HandleMessage) succeeds, second call (title generation) fails
	if m.callCount == 1 {
		return &provider.ChatResponse{Content: m.response}, nil
	}
	return nil, errors.New("mock error")
}
func (m *mockErrorProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Delta: m.response, Done: true}
	close(ch)
	return ch, nil
}

func TestTitleGeneration_FallbackOnError(t *testing.T) {
	// Create manager with error provider for title generation.
	st := testutil.NewMemoryStore(100)
	prov := &mockErrorProvider{response: "Stream response"}
	eng := &engine.Engine{Provider: prov}
	mgr := session.NewManager(session.ManagerConfig{
		Store:      st,
		Engine:     eng,
		Model:      "mock-model",
		TokenLimit: 128000,
		Threshold:  0.8,
	})
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "Help me fix the WebSocket reconnect issue", session.HandleOptions{})

	// Poll with timeout.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		sess, _ := st.ActiveSession("ws-test")
		if sess != nil && sess.Title != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	sess, _ := st.ActiveSession("ws-test")
	if sess == nil {
		t.Fatal("no session")
	}
	// Title should be set to truncated user message as fallback.
	expected := "Help me fix the WebSocket reconnect issue"
	if sess.Title != expected {
		t.Errorf("expected fallback title %q, got %q", expected, sess.Title)
	}
}

func TestTitleGeneration_EmptyResponse(t *testing.T) {
	// Mock provider returns empty/whitespace.
	mgr, st := newTestManager(t, "   ")
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "Test message", session.HandleOptions{})

	// Poll with timeout.
	deadline := time.Now().Add(1 * time.Second)
	var sess *store.Session
	for time.Now().Before(deadline) {
		sess, _ = st.ActiveSession("ws-test")
		if sess != nil {
			// Wait a bit more to ensure async title generation completes.
			time.Sleep(50 * time.Millisecond)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	sess, _ = st.ActiveSession("ws-test")
	if sess == nil {
		t.Fatal("no session")
	}
	// Title should remain empty when response is whitespace.
	if sess.Title != "" {
		t.Errorf("expected empty title for whitespace response, got %q", sess.Title)
	}
}
