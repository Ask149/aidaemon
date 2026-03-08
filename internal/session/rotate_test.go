package session_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/session"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/testutil"
	"github.com/Ask149/aidaemon/internal/tools"
)

func TestRotateSession_CarriesSummary(t *testing.T) {
	st := testutil.NewMemoryStore(100)
	prov := &mockProvider{response: "Summary of conversation."}
	eng := &engine.Engine{Provider: prov, Registry: tools.NewRegistry(nil)}

	wsDir := t.TempDir()
	mgr := session.NewManager(session.ManagerConfig{
		Store:        st,
		Engine:       eng,
		Model:        "mock-model",
		TokenLimit:   128000,
		Threshold:    0.8,
		WorkspaceDir: wsDir,
	})
	ctx := context.Background()

	// Seed conversation.
	mgr.HandleMessage(ctx, "ws-test", "Build me a web app", session.HandleOptions{})
	mgr.HandleMessage(ctx, "ws-test", "Add user auth", session.HandleOptions{})

	// Rotate — should summarize and carry forward.
	newID, err := mgr.RotateSession(ctx, "ws-test")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Old session should have summary.
	sessions, _ := st.ListAllSessions("ws-test")
	var oldSess *store.Session
	for i := range sessions {
		if sessions[i].ID != newID {
			oldSess = &sessions[i]
			break
		}
	}
	if oldSess == nil || oldSess.Summary == "" {
		t.Error("expected old session to have summary")
	}

	// New session should have carry-forward message.
	history, _ := st.GetHistory(newID)
	if len(history) == 0 {
		t.Fatal("expected carry-forward message in new session")
	}
	found := false
	for _, msg := range history {
		if strings.Contains(msg.Content, "Carried forward") || strings.Contains(msg.Content, "previous session") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected carry-forward message")
	}
}

func TestRotateSession_WritesDailyLog(t *testing.T) {
	st := testutil.NewMemoryStore(100)
	prov := &mockProvider{response: "Summary text."}
	eng := &engine.Engine{Provider: prov, Registry: tools.NewRegistry(nil)}

	wsDir := t.TempDir()
	mgr := session.NewManager(session.ManagerConfig{
		Store:        st,
		Engine:       eng,
		Model:        "mock-model",
		TokenLimit:   128000,
		Threshold:    0.8,
		WorkspaceDir: wsDir,
	})
	ctx := context.Background()

	mgr.HandleMessage(ctx, "ws-test", "hello", session.HandleOptions{})
	mgr.RotateSession(ctx, "ws-test")

	// Check that a daily log file was created.
	entries, err := os.ReadDir(filepath.Join(wsDir, "memory"))
	if err != nil {
		t.Fatalf("read memory dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected daily log file in memory/")
	}
	// File should contain the summary.
	data, _ := os.ReadFile(filepath.Join(wsDir, "memory", entries[0].Name()))
	if !strings.Contains(string(data), "Summary") {
		t.Errorf("expected summary in daily log, got: %s", string(data))
	}
}
