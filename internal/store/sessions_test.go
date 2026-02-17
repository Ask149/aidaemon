package store

import (
	"testing"
	"time"
)

// TestCreateAndGetSession verifies we can create a session and retrieve it.
func TestCreateAndGetSession(t *testing.T) {
	st := tempStore(t, 100)

	sess := Session{
		ID:            "s_test123",
		Channel:       "ws-alice",
		Title:         "Initial chat",
		Status:        "active",
		TokenEstimate: 1200,
		CreatedAt:     time.Now().UTC(),
		LastActivity:  time.Now().UTC(),
	}

	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := st.GetSession("s_test123")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil")
	}

	if got.ID != sess.ID {
		t.Errorf("ID = %q, want %q", got.ID, sess.ID)
	}
	if got.Channel != sess.Channel {
		t.Errorf("Channel = %q, want %q", got.Channel, sess.Channel)
	}
	if got.Title != sess.Title {
		t.Errorf("Title = %q, want %q", got.Title, sess.Title)
	}
	if got.Status != sess.Status {
		t.Errorf("Status = %q, want %q", got.Status, sess.Status)
	}
	if got.TokenEstimate != sess.TokenEstimate {
		t.Errorf("TokenEstimate = %d, want %d", got.TokenEstimate, sess.TokenEstimate)
	}
	if got.ClosedAt != nil {
		t.Errorf("ClosedAt = %v, want nil", got.ClosedAt)
	}
}

// TestActiveSession verifies we can find the active session for a channel.
func TestActiveSession(t *testing.T) {
	st := tempStore(t, 100)

	// No active session initially.
	active, err := st.ActiveSession("ws-bob")
	if err != nil {
		t.Fatalf("ActiveSession (empty): %v", err)
	}
	if active != nil {
		t.Errorf("ActiveSession (empty) = %+v, want nil", active)
	}

	// Create an active session.
	sess1 := Session{
		ID:           "s_active",
		Channel:      "ws-bob",
		Status:       "active",
		CreatedAt:    time.Now().UTC(),
		LastActivity: time.Now().UTC(),
	}
	if err := st.CreateSession(sess1); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Should find it.
	active, err = st.ActiveSession("ws-bob")
	if err != nil {
		t.Fatalf("ActiveSession: %v", err)
	}
	if active == nil {
		t.Fatal("ActiveSession returned nil")
	}
	if active.ID != "s_active" {
		t.Errorf("ActiveSession ID = %q, want s_active", active.ID)
	}

	// Create a closed session for the same channel.
	sess2 := Session{
		ID:           "s_closed",
		Channel:      "ws-bob",
		Status:       "closed",
		CreatedAt:    time.Now().UTC(),
		LastActivity: time.Now().UTC(),
	}
	if err := st.CreateSession(sess2); err != nil {
		t.Fatalf("CreateSession (closed): %v", err)
	}

	// Should still find the active one.
	active, err = st.ActiveSession("ws-bob")
	if err != nil {
		t.Fatalf("ActiveSession (after closed): %v", err)
	}
	if active == nil || active.ID != "s_active" {
		t.Errorf("ActiveSession = %v, want s_active", active)
	}

	// Different channel should return nil.
	active, err = st.ActiveSession("ws-charlie")
	if err != nil {
		t.Fatalf("ActiveSession (other channel): %v", err)
	}
	if active != nil {
		t.Errorf("ActiveSession (other channel) = %+v, want nil", active)
	}
}

// TestUpdateSession verifies we can update session metadata.
func TestUpdateSession(t *testing.T) {
	st := tempStore(t, 100)

	sess := Session{
		ID:            "s_update",
		Channel:       "ws-dave",
		Status:        "active",
		TokenEstimate: 500,
		CreatedAt:     time.Now().UTC(),
		LastActivity:  time.Now().UTC(),
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Update title, status, summary, token estimate.
	closedAt := time.Now().UTC()
	sess.Title = "Debugging session"
	sess.Status = "closed"
	sess.Summary = "Fixed auth bug"
	sess.TokenEstimate = 3000
	sess.ClosedAt = &closedAt

	if err := st.UpdateSession(sess); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, err := st.GetSession("s_update")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Title != "Debugging session" {
		t.Errorf("Title = %q, want 'Debugging session'", got.Title)
	}
	if got.Status != "closed" {
		t.Errorf("Status = %q, want 'closed'", got.Status)
	}
	if got.Summary != "Fixed auth bug" {
		t.Errorf("Summary = %q, want 'Fixed auth bug'", got.Summary)
	}
	if got.TokenEstimate != 3000 {
		t.Errorf("TokenEstimate = %d, want 3000", got.TokenEstimate)
	}
	if got.ClosedAt == nil {
		t.Error("ClosedAt = nil, want non-nil")
	}
}

// TestListAllSessions verifies we can list all sessions, optionally filtered by channel.
func TestListAllSessions(t *testing.T) {
	st := tempStore(t, 100)

	now := time.Now().UTC()

	sessions := []Session{
		{ID: "s_1", Channel: "ws-alice", Status: "closed", CreatedAt: now.Add(-3 * time.Hour), LastActivity: now.Add(-3 * time.Hour)},
		{ID: "s_2", Channel: "ws-alice", Status: "active", CreatedAt: now.Add(-2 * time.Hour), LastActivity: now.Add(-1 * time.Hour)},
		{ID: "s_3", Channel: "ws-bob", Status: "closed", CreatedAt: now.Add(-1 * time.Hour), LastActivity: now.Add(-30 * time.Minute)},
		{ID: "s_4", Channel: "ws-bob", Status: "active", CreatedAt: now, LastActivity: now},
	}

	for _, s := range sessions {
		if err := st.CreateSession(s); err != nil {
			t.Fatalf("CreateSession(%s): %v", s.ID, err)
		}
	}

	// List all sessions (newest first).
	all, err := st.ListAllSessions("")
	if err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("ListAllSessions count = %d, want 4", len(all))
	}
	// Should be ordered by last_activity DESC.
	if all[0].ID != "s_4" || all[1].ID != "s_3" || all[2].ID != "s_2" || all[3].ID != "s_1" {
		t.Errorf("ListAllSessions order = [%s, %s, %s, %s], want [s_4, s_3, s_2, s_1]",
			all[0].ID, all[1].ID, all[2].ID, all[3].ID)
	}

	// List only ws-alice sessions.
	alice, err := st.ListAllSessions("ws-alice")
	if err != nil {
		t.Fatalf("ListAllSessions(ws-alice): %v", err)
	}
	if len(alice) != 2 {
		t.Fatalf("ListAllSessions(ws-alice) count = %d, want 2", len(alice))
	}
	if alice[0].ID != "s_2" || alice[1].ID != "s_1" {
		t.Errorf("ListAllSessions(ws-alice) order = [%s, %s], want [s_2, s_1]", alice[0].ID, alice[1].ID)
	}

	// List only ws-bob sessions.
	bob, err := st.ListAllSessions("ws-bob")
	if err != nil {
		t.Fatalf("ListAllSessions(ws-bob): %v", err)
	}
	if len(bob) != 2 {
		t.Fatalf("ListAllSessions(ws-bob) count = %d, want 2", len(bob))
	}
	if bob[0].ID != "s_4" || bob[1].ID != "s_3" {
		t.Errorf("ListAllSessions(ws-bob) order = [%s, %s], want [s_4, s_3]", bob[0].ID, bob[1].ID)
	}
}

// TestSessionMigration verifies that existing chat_id values are migrated to sessions.
func TestSessionMigration(t *testing.T) {
	st := tempStore(t, 100)

	// Create some messages with old-style chat IDs.
	if err := st.AddMessage("ws-alice:old_chat_1", "user", "Hello"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if err := st.AddMessage("ws-alice:old_chat_1", "assistant", "Hi there"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if err := st.AddMessage("ws-bob:old_chat_2", "user", "Test"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	// Run migration.
	if err := st.MigrateExistingSessions(); err != nil {
		t.Fatalf("MigrateExistingSessions: %v", err)
	}

	// Check sessions were created.
	sessions, err := st.ListAllSessions("")
	if err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}

	if len(sessions) < 2 {
		t.Fatalf("ListAllSessions count = %d, want at least 2", len(sessions))
	}

	// Find the migrated sessions.
	var aliceSess, bobSess *Session
	for i := range sessions {
		if sessions[i].ID == "ws-alice:old_chat_1" {
			aliceSess = &sessions[i]
		}
		if sessions[i].ID == "ws-bob:old_chat_2" {
			bobSess = &sessions[i]
		}
	}

	if aliceSess == nil {
		t.Error("Migration did not create session for ws-alice:old_chat_1")
	} else {
		if aliceSess.Channel != "ws-alice" {
			t.Errorf("aliceSess.Channel = %q, want 'ws-alice'", aliceSess.Channel)
		}
		if aliceSess.Status != "active" {
			t.Errorf("aliceSess.Status = %q, want 'active'", aliceSess.Status)
		}
	}

	if bobSess == nil {
		t.Error("Migration did not create session for ws-bob:old_chat_2")
	} else {
		if bobSess.Channel != "ws-bob" {
			t.Errorf("bobSess.Channel = %q, want 'ws-bob'", bobSess.Channel)
		}
	}

	// Run migration again — should be idempotent.
	if err := st.MigrateExistingSessions(); err != nil {
		t.Fatalf("MigrateExistingSessions (second): %v", err)
	}

	sessions2, err := st.ListAllSessions("")
	if err != nil {
		t.Fatalf("ListAllSessions (after second migration): %v", err)
	}
	if len(sessions2) != len(sessions) {
		t.Errorf("Second migration created duplicates: got %d sessions, want %d", len(sessions2), len(sessions))
	}
}

// tempStore creates a temporary SQLite store for testing.
func tempStore(t *testing.T, limit int) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	st, err := New(dir+"/test.db", limit)
	if err != nil {
		t.Fatalf("tempStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
