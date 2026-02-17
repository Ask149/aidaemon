package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/store"
)

// mockSessionManager implements the SessionManager interface for testing.
type mockSessionManager struct {
	sessions map[string]*store.Session
}

func (m *mockSessionManager) GetSession(id string) (*store.Session, error) {
	sess, ok := m.sessions[id]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

func (m *mockSessionManager) ListSessions(channel string) ([]store.Session, error) {
	var result []store.Session
	for _, sess := range m.sessions {
		if channel == "" || sess.Channel == channel {
			result = append(result, *sess)
		}
	}
	return result, nil
}

func (m *mockSessionManager) RenameSession(id, title string) error {
	sess, ok := m.sessions[id]
	if !ok {
		return nil
	}
	sess.Title = title
	return nil
}

// mockStore implements minimal Store interface methods needed for testing.
type mockStore struct {
	sessions map[string]*store.Session
	messages map[string][]store.Message
}

func (m *mockStore) GetSession(id string) (*store.Session, error) {
	sess, ok := m.sessions[id]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

func (m *mockStore) UpdateSession(sess store.Session) error {
	m.sessions[sess.ID] = &sess
	return nil
}

func (m *mockStore) GetHistory(chatID string) ([]store.Message, error) {
	msgs, ok := m.messages[chatID]
	if !ok {
		return []store.Message{}, nil
	}
	return msgs, nil
}

func (m *mockStore) ListSessions() ([]store.SessionInfo, error) {
	var result []store.SessionInfo
	for id, sess := range m.sessions {
		msgCount := len(m.messages[id])
		result = append(result, store.SessionInfo{
			ChatID:       id,
			MessageCount: msgCount,
			LastActivity: sess.LastActivity,
		})
	}
	return result, nil
}

func TestHandleGetSession(t *testing.T) {
	now := time.Now()
	mockSess := &mockSessionManager{
		sessions: map[string]*store.Session{
			"test-123": {
				ID:           "test-123",
				Channel:      "cli",
				Title:        "Test Session",
				Status:       "active",
				CreatedAt:    now,
				LastActivity: now,
			},
		},
	}

	api := &API{
		cfg: Config{
			Token:          "test-token",
			SessionManager: mockSess,
		},
	}

	tests := []struct {
		name           string
		sessionID      string
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "existing session",
			sessionID:      "test-123",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "non-existing session",
			sessionID:      "not-found",
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/sessions/"+tt.sessionID, nil)
			req.SetPathValue("id", tt.sessionID)
			w := httptest.NewRecorder()

			api.handleGetSession(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if !tt.expectError {
				var sess store.Session
				if err := json.NewDecoder(w.Body).Decode(&sess); err != nil {
					t.Errorf("failed to decode response: %v", err)
				}
				if sess.ID != tt.sessionID {
					t.Errorf("expected session ID %s, got %s", tt.sessionID, sess.ID)
				}
			}
		})
	}
}

func TestHandleGetSessionMessages(t *testing.T) {
	// Skip this test for now since we can't easily mock the Store interface without CGO
	t.Skip("Skipping message retrieval test - requires full Store mock")
}

func TestHandleRenameSession(t *testing.T) {
	now := time.Now()
	mockSess := &mockSessionManager{
		sessions: map[string]*store.Session{
			"test-123": {
				ID:           "test-123",
				Channel:      "cli",
				Title:        "Old Title",
				Status:       "active",
				CreatedAt:    now,
				LastActivity: now,
			},
		},
	}

	api := &API{
		cfg: Config{
			Token:          "test-token",
			SessionManager: mockSess,
		},
	}

	tests := []struct {
		name           string
		sessionID      string
		newTitle       string
		expectedStatus int
	}{
		{
			name:           "rename existing session",
			sessionID:      "test-123",
			newTitle:       "New Title",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "rename non-existing session (graceful)",
			sessionID:      "not-found",
			newTitle:       "Some Title",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"title": tt.newTitle})
			req := httptest.NewRequest("POST", "/sessions/"+tt.sessionID+"/title", bytes.NewReader(body))
			req.SetPathValue("id", tt.sessionID)
			w := httptest.NewRecorder()

			api.handleRenameSession(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			// Verify title was updated for existing session
			if tt.sessionID == "test-123" {
				if mockSess.sessions[tt.sessionID].Title != tt.newTitle {
					t.Errorf("expected title to be updated to %s, got %s",
						tt.newTitle, mockSess.sessions[tt.sessionID].Title)
				}
			}
		})
	}
}

func TestHandleRenameSessionInvalidBody(t *testing.T) {
	api := &API{
		cfg: Config{
			Token: "test-token",
		},
	}

	req := httptest.NewRequest("POST", "/sessions/test-123/title", bytes.NewReader([]byte("invalid json")))
	req.SetPathValue("id", "test-123")
	w := httptest.NewRecorder()

	api.handleRenameSession(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandleSessionsWithSessionManager(t *testing.T) {
	now := time.Now()
	mockSess := &mockSessionManager{
		sessions: map[string]*store.Session{
			"test-1": {
				ID:           "test-1",
				Channel:      "cli",
				Title:        "Session 1",
				Status:       "active",
				CreatedAt:    now,
				LastActivity: now,
			},
			"test-2": {
				ID:           "test-2",
				Channel:      "web",
				Title:        "Session 2",
				Status:       "closed",
				CreatedAt:    now,
				LastActivity: now,
			},
		},
	}

	api := &API{
		cfg: Config{
			Token:          "test-token",
			SessionManager: mockSess,
		},
	}

	req := httptest.NewRequest("GET", "/sessions", nil)
	w := httptest.NewRecorder()

	api.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var sessions []store.Session
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Errorf("failed to decode response: %v", err)
	}

	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}
