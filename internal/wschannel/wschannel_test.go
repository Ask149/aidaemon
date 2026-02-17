package wschannel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// helper to dial a test WebSocket server.
func dialTest(t *testing.T, server *httptest.Server) (*websocket.Conn, context.Context, context.CancelFunc) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	return conn, ctx, cancel
}

func TestName(t *testing.T) {
	ch := New(Config{})
	if got := ch.Name(); got != "websocket" {
		t.Errorf("Name() = %q, want %q", got, "websocket")
	}
}

func TestWebSocketChannel_Echo(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "echo: " + text, nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Send a message.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read response.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Reply != "echo: hello" {
		t.Errorf("reply = %q, want %q", msg.Reply, "echo: hello")
	}
}

func TestWebSocketChannel_InvalidJSON(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			t.Error("OnMessage should not be called for invalid JSON")
			return "", nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Send invalid JSON.
	err := conn.Write(ctx, websocket.MessageText, []byte(`not json`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Expect error response.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "invalid JSON" {
		t.Errorf("error = %q, want %q", msg.Error, "invalid JSON")
	}
}

func TestWebSocketChannel_Send(t *testing.T) {
	// Block OnMessage so we can call Send from the test goroutine.
	onMsgCalled := make(chan string, 1)
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			onMsgCalled <- sessionID
			// Block until test is done reading the Send message.
			<-ctx.Done()
			return "", ctx.Err()
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Send a message to trigger OnMessage and learn the session ID.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Wait for OnMessage to fire and capture the session ID.
	var sid string
	select {
	case sid = <-onMsgCalled:
	case <-ctx.Done():
		t.Fatal("timeout waiting for OnMessage")
	}

	// Use Send to push a server-initiated message.
	if err := ch.Send(ctx, sid, "pushed"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read the pushed message.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Reply != "pushed" {
		t.Errorf("reply = %q, want %q", msg.Reply, "pushed")
	}
}

func TestWebSocketChannel_SendDisconnected(t *testing.T) {
	ch := New(Config{})
	// Sending to an unknown session should not error.
	err := ch.Send(context.Background(), "no-such-session", "hello")
	if err != nil {
		t.Errorf("Send to disconnected session: %v", err)
	}
}

func TestWebSocketChannel_SessionIDMutation(t *testing.T) {
	received := make(chan string, 1)
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			received <- sessionID
			return "ok", nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Send a message with an explicit session_id.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi","session_id":"custom-123"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read the response to flush the handler loop.
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// OnMessage should have received the custom session ID.
	select {
	case sid := <-received:
		if sid != "custom-123" {
			t.Errorf("sessionID = %q, want %q", sid, "custom-123")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for OnMessage")
	}

	// Verify Send works with the new session ID (conn map key was updated).
	if err := ch.Send(ctx, "custom-123", "pushed"); err != nil {
		t.Fatalf("Send with custom session: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read pushed: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Reply != "pushed" {
		t.Errorf("reply = %q, want %q", msg.Reply, "pushed")
	}
}

func TestWebSocketChannel_OnMessageError(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "", fmt.Errorf("something broke")
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "something broke" {
		t.Errorf("error = %q, want %q", msg.Error, "something broke")
	}
}

func TestWebSocketChannel_SendImage(t *testing.T) {
	// Block OnMessage so we can call SendImage from the test goroutine.
	onMsgCalled := make(chan string, 1)
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			onMsgCalled <- sessionID
			<-ctx.Done()
			return "", ctx.Err()
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Send a message to trigger OnMessage and learn the session ID.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	var sid string
	select {
	case sid = <-onMsgCalled:
	case <-ctx.Done():
		t.Fatal("timeout waiting for OnMessage")
	}

	// Use SendImage to push an image message.
	dataURL := "data:image/png;base64,iVBOR"
	if err := ch.SendImage(ctx, sid, dataURL); err != nil {
		t.Fatalf("SendImage: %v", err)
	}

	// Read the image message.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Image != dataURL {
		t.Errorf("image = %q, want %q", msg.Image, dataURL)
	}
	if msg.Reply != "" {
		t.Errorf("reply should be empty, got %q", msg.Reply)
	}
}

func TestWebSocketChannel_SendImageDisconnected(t *testing.T) {
	ch := New(Config{})
	// Sending an image to an unknown session should not error.
	err := ch.SendImage(context.Background(), "no-such-session", "data:image/png;base64,AA")
	if err != nil {
		t.Errorf("SendImage to disconnected session: %v", err)
	}
}

func TestWebSocketChannel_CommandNew(t *testing.T) {
	var receivedSessionID string
	newSessionCalled := make(chan struct{})
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		OnNewSession: func(ctx context.Context, sessionID string) (string, error) {
			receivedSessionID = sessionID
			close(newSessionCalled)
			return "new-session-456", nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// First, establish a known session ID.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi","session_id":"session-123"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// Consume the response.
	conn.Read(ctx)

	// Send /new command.
	err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"new"}`))
	if err != nil {
		t.Fatalf("write command: %v", err)
	}

	// Wait for OnNewSession to be called.
	select {
	case <-newSessionCalled:
		if receivedSessionID != "session-123" {
			t.Errorf("OnNewSession received sessionID = %q, want %q", receivedSessionID, "session-123")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for OnNewSession")
	}

	// Verify the connection was rotated to the new session ID.
	err = ch.Send(ctx, "new-session-456", "rotated")
	if err != nil {
		t.Fatalf("Send to new session: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Reply != "rotated" {
		t.Errorf("reply = %q, want %q", msg.Reply, "rotated")
	}
}

func TestWebSocketChannel_CommandNewError(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		OnNewSession: func(ctx context.Context, sessionID string) (string, error) {
			return "", fmt.Errorf("failed to create session")
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"new"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "failed to create session" {
		t.Errorf("error = %q, want %q", msg.Error, "failed to create session")
	}
}

func TestWebSocketChannel_CommandNewNotSupported(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		// OnNewSession not set.
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"new"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "new session not supported" {
		t.Errorf("error = %q, want %q", msg.Error, "new session not supported")
	}
}

func TestWebSocketChannel_CommandTitle(t *testing.T) {
	var receivedSessionID, receivedTitle string
	titleCalled := make(chan struct{})
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		OnRenameSession: func(ctx context.Context, sessionID, title string) error {
			receivedSessionID = sessionID
			receivedTitle = title
			close(titleCalled)
			return nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Establish a known session ID.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi","session_id":"session-789"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// Consume the response.
	conn.Read(ctx)

	// Send /title command.
	err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"title","text":"My New Title"}`))
	if err != nil {
		t.Fatalf("write command: %v", err)
	}

	// Wait for OnRenameSession to be called.
	select {
	case <-titleCalled:
		if receivedSessionID != "session-789" {
			t.Errorf("OnRenameSession received sessionID = %q, want %q", receivedSessionID, "session-789")
		}
		if receivedTitle != "My New Title" {
			t.Errorf("OnRenameSession received title = %q, want %q", receivedTitle, "My New Title")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for OnRenameSession")
	}
}

func TestWebSocketChannel_CommandTitleMissingText(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		OnRenameSession: func(ctx context.Context, sessionID, title string) error {
			t.Error("OnRenameSession should not be called when text is missing")
			return nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"title"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "title text required" {
		t.Errorf("error = %q, want %q", msg.Error, "title text required")
	}
}

func TestWebSocketChannel_CommandTitleError(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		OnRenameSession: func(ctx context.Context, sessionID, title string) error {
			return fmt.Errorf("rename failed")
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"title","text":"test"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "rename failed" {
		t.Errorf("error = %q, want %q", msg.Error, "rename failed")
	}
}

func TestWebSocketChannel_CommandTitleNotSupported(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
		// OnRenameSession not set.
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"title","text":"test"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "rename session not supported" {
		t.Errorf("error = %q, want %q", msg.Error, "rename session not supported")
	}
}

func TestWebSocketChannel_CommandUnknown(t *testing.T) {
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "ok", nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"command","command":"unknown"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Error != "unknown command: unknown" {
		t.Errorf("error = %q, want %q", msg.Error, "unknown command: unknown")
	}
}

func TestWebSocketChannel_SendSessionRotated(t *testing.T) {
	onMsgCalled := make(chan string, 1)
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			onMsgCalled <- sessionID
			<-ctx.Done()
			return "", ctx.Err()
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	conn, ctx, cancel := dialTest(t, server)
	defer cancel()
	defer conn.CloseNow()

	// Trigger connection and get session ID.
	err := conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hi"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	var sid string
	select {
	case sid = <-onMsgCalled:
	case <-ctx.Done():
		t.Fatal("timeout waiting for OnMessage")
	}

	// Send session_rotated event.
	if err := ch.SendSessionRotated(ctx, sid, "new-sess-999", "New Session Title"); err != nil {
		t.Fatalf("SendSessionRotated: %v", err)
	}

	// Read the event.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != "session_rotated" {
		t.Errorf("type = %q, want %q", msg.Type, "session_rotated")
	}
	if msg.SessionID != "new-sess-999" {
		t.Errorf("session_id = %q, want %q", msg.SessionID, "new-sess-999")
	}
	if msg.Title != "New Session Title" {
		t.Errorf("title = %q, want %q", msg.Title, "New Session Title")
	}
}

func TestWebSocketChannel_SendSessionRotatedDisconnected(t *testing.T) {
	ch := New(Config{})
	// Sending to an unknown session should not error.
	err := ch.SendSessionRotated(context.Background(), "no-such-session", "new-sess", "title")
	if err != nil {
		t.Errorf("SendSessionRotated to disconnected session: %v", err)
	}
}
