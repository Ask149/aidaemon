// Package session_test contains tests for session lifecycle operations.
package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/session"
)

func TestTitleGeneration(t *testing.T) {
	// Mock provider returns "Debugging WebSocket" as title.
	mgr, st := newTestManager(t, "Debugging WebSocket")
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "Help me fix the WebSocket reconnect", session.HandleOptions{})

	// Allow async title generation.
	time.Sleep(100 * time.Millisecond)

	sess, _ := st.ActiveSession("ws-test")
	if sess == nil {
		t.Fatal("no session")
	}
	// Title should be set (mock returns "Debugging WebSocket" for all calls).
	if sess.Title == "" {
		t.Error("expected title to be generated")
	}
}
