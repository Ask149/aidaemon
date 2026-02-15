package wschannel

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestWebSocketChannel_Echo(t *testing.T) {
	// Create a test handler that echoes messages.
	ch := New(Config{
		OnMessage: func(ctx context.Context, sessionID, text string) (string, error) {
			return "echo: " + text, nil
		},
	})

	server := httptest.NewServer(ch.Handler())
	defer server.Close()

	// Connect via WebSocket.
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send a message.
	err = conn.Write(ctx, websocket.MessageText, []byte(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read response.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if !strings.Contains(string(data), "echo: hello") {
		t.Errorf("unexpected response: %s", string(data))
	}
}
