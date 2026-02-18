package teams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/channel"
)

// TestStripHTML is a table-driven test for the HTML tag stripper.
func TestStripHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"paragraph tags", "<p>hello</p>", "hello"},
		{"bold and italic", "<b>bold</b> and <i>italic</i>", "bold and italic"},
		{"no tags", "no tags", "no tags"},
		{"nested tags", "<div><p>nested</p></div>", "nested"},
		{"html entities", "&amp; &lt; &gt;", "& < >"},
		{"empty string", "", ""},
		{"nbsp entity", "hello&nbsp;world", "hello world"},
		{"quot entity", "&quot;quoted&quot;", "\"quoted\""},
		{"mixed tags and entities", "<p>&amp; hello &lt;world&gt;</p>", "& hello <world>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSkipOwnMessages verifies that messages from self are not delivered to OnMessage.
func TestSkipOwnMessages(t *testing.T) {
	var called bool
	ch := &Channel{
		cfg: Config{
			ChatID:       "test-chat-id",
			PollInterval: time.Second,
			OnMessage: func(ctx context.Context, sessionID string, text string) {
				called = true
			},
		},
		userID: "user-123",
	}

	// Simulate a message from self (sender ID matches userID).
	msgs := []graphMessage{
		{
			ID:              "msg-1",
			CreatedDateTime: time.Now().Format(time.RFC3339),
			Body:            graphMessageBody{Content: "hello from self", ContentType: "text"},
			From: graphMessageFrom{
				User: graphUser{ID: "user-123"},
			},
		},
	}

	ch.processMessages(context.Background(), msgs)

	if called {
		t.Error("OnMessage was called for a self-message; expected it to be skipped")
	}
}

// TestProcessNewMessages verifies that new messages are processed, HTML is stripped,
// and duplicate messages are not reprocessed.
func TestProcessNewMessages(t *testing.T) {
	// Track messages delivered to OnMessage.
	var mu sync.Mutex
	var received []struct {
		sessionID string
		text      string
	}

	chatID := "test-chat-id"

	// Mock Graph API server.
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1.0/me":
			json.NewEncoder(w).Encode(map[string]string{"id": "my-user-id"})

		case strings.HasSuffix(r.URL.Path, "/messages"):
			callCount++
			// Always return the same messages. Second call should NOT re-trigger OnMessage.
			resp := graphMessagesResponse{
				Value: []graphMessage{
					{
						ID:              "msg-1",
						CreatedDateTime: "2025-01-01T10:00:00Z",
						Body:            graphMessageBody{Content: "<p>hello world</p>", ContentType: "html"},
						From:            graphMessageFrom{User: graphUser{ID: "other-user"}},
					},
					{
						ID:              "msg-2",
						CreatedDateTime: "2025-01-01T10:01:00Z",
						Body:            graphMessageBody{Content: "<b>important</b> &amp; urgent", ContentType: "html"},
						From:            graphMessageFrom{User: graphUser{ID: "other-user"}},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ch := &Channel{
		cfg: Config{
			ChatID:       chatID,
			PollInterval: 50 * time.Millisecond,
			OnMessage: func(ctx context.Context, sessionID string, text string) {
				mu.Lock()
				defer mu.Unlock()
				received = append(received, struct {
					sessionID string
					text      string
				}{sessionID, text})
			},
		},
		graphBaseURL: server.URL + "/v1.0",
		httpClient:   server.Client(),
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start in a goroutine — it blocks until ctx cancelled.
	errCh := make(chan error, 1)
	go func() {
		errCh <- ch.Start(ctx)
	}()

	// Wait for at least 2 poll cycles.
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Wait for Start to return.
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have received exactly 2 messages (from first poll only).
	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(received))
	}

	// Verify HTML was stripped.
	if received[0].text != "hello world" {
		t.Errorf("message 0: got %q, want %q", received[0].text, "hello world")
	}
	if received[1].text != "important & urgent" {
		t.Errorf("message 1: got %q, want %q", received[1].text, "important & urgent")
	}

	// Verify session ID format.
	expectedSessionID := channel.SessionID("teams", chatID)
	if received[0].sessionID != expectedSessionID {
		t.Errorf("session ID: got %q, want %q", received[0].sessionID, expectedSessionID)
	}
}

// TestSend verifies that Send POSTs the correct JSON body to the Graph API.
func TestSend(t *testing.T) {
	var gotBody map[string]interface{}
	var gotPath string
	var gotAuth string

	chatID := "test-chat-id"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "sent-msg-1"})
	}))
	defer server.Close()

	ch := &Channel{
		cfg: Config{
			ChatID: chatID,
		},
		graphBaseURL: server.URL + "/v1.0",
		httpClient:   server.Client(),
	}

	err := ch.Send(context.Background(), channel.SessionID("teams", chatID), "Hello from aidaemon")
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	// Verify path.
	expectedPath := "/v1.0/me/chats/" + chatID + "/messages"
	if gotPath != expectedPath {
		t.Errorf("POST path: got %q, want %q", gotPath, expectedPath)
	}

	// Verify Authorization header uses token from getToken().
	// Without a TokenManager, getToken() returns "" — header is "Bearer ".
	if !strings.HasPrefix(gotAuth, "Bearer") {
		t.Errorf("Authorization: got %q, want prefix %q", gotAuth, "Bearer")
	}

	// Verify body structure.
	body, ok := gotBody["body"].(map[string]interface{})
	if !ok {
		t.Fatalf("body field missing or wrong type: %v", gotBody)
	}
	if body["content"] != "Hello from aidaemon" {
		t.Errorf("body.content: got %q, want %q", body["content"], "Hello from aidaemon")
	}
	if body["contentType"] != "text" {
		t.Errorf("body.contentType: got %q, want %q", body["contentType"], "text")
	}
}

// TestName verifies the channel name.
func TestName(t *testing.T) {
	ch := &Channel{}
	if ch.Name() != "teams" {
		t.Errorf("Name() = %q, want %q", ch.Name(), "teams")
	}
}

// TestSkipSystemMessages verifies that system messages (no sender) are not delivered.
func TestSkipSystemMessages(t *testing.T) {
	var called bool
	ch := &Channel{
		cfg: Config{
			ChatID:       "test-chat-id",
			PollInterval: time.Second,
			OnMessage: func(ctx context.Context, sessionID string, text string) {
				called = true
			},
		},
		userID: "user-123",
	}

	// System message has empty From.User.ID (zero-value struct).
	msgs := []graphMessage{
		{
			ID:              "msg-sys",
			CreatedDateTime: time.Now().Format(time.RFC3339),
			Body:            graphMessageBody{Content: "Meeting started", ContentType: "text"},
			From:            graphMessageFrom{User: graphUser{ID: ""}},
		},
	}

	ch.processMessages(context.Background(), msgs)

	if called {
		t.Error("OnMessage was called for a system message; expected it to be skipped")
	}
}
