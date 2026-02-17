// Package wschannel provides a WebSocket transport for the chat gateway.
//
// It accepts WebSocket connections via an HTTP handler, tracks connected
// clients by session ID, and delegates incoming messages to a callback.
// Messages use a JSON protocol: {"message":"..."} inbound, {"reply":"..."}
// or {"error":"..."} outbound.
package wschannel

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/Ask149/aidaemon/internal/channel"
	"nhooyr.io/websocket"
)

// Compile-time interface check.
var _ channel.Channel = (*Channel)(nil)

// OnMessageFunc handles an incoming message and returns a reply.
type OnMessageFunc func(ctx context.Context, sessionID, text string) (string, error)

// Config for the WebSocket channel.
type Config struct {
	OnMessage OnMessageFunc
}

// Channel handles WebSocket connections for chat.
type Channel struct {
	cfg   Config
	mu    sync.RWMutex
	conns map[string]*websocket.Conn // sessionID → connection
}

// New creates a WebSocket channel.
func New(cfg Config) *Channel {
	return &Channel{
		cfg:   cfg,
		conns: make(map[string]*websocket.Conn),
	}
}

// Name returns the channel name.
func (c *Channel) Name() string { return "websocket" }

// Start is a no-op for WebSocket; the HTTP server drives connections.
func (c *Channel) Start(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Handler returns an http.HandlerFunc for WebSocket upgrades.
func (c *Channel) Handler() http.HandlerFunc {
	return c.handleWS
}

// Send delivers a message to a connected WebSocket client.
func (c *Channel) Send(ctx context.Context, sessionID string, text string) error {
	c.mu.RLock()
	conn, ok := c.conns[sessionID]
	c.mu.RUnlock()
	if !ok {
		return nil // Client disconnected, silently skip.
	}

	msg := wsMessage{Reply: text}
	data, _ := json.Marshal(msg)
	return conn.Write(ctx, websocket.MessageText, data)
}

// SendImage delivers an image to a connected WebSocket client as a data URL.
// The image is sent as a separate JSON message with the "image" field.
func (c *Channel) SendImage(ctx context.Context, sessionID string, dataURL string) error {
	c.mu.RLock()
	conn, ok := c.conns[sessionID]
	c.mu.RUnlock()
	if !ok {
		return nil // Client disconnected, silently skip.
	}

	msg := wsMessage{Image: dataURL}
	data, _ := json.Marshal(msg)
	return conn.Write(ctx, websocket.MessageText, data)
}

type wsIncoming struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id,omitempty"`
}

type wsMessage struct {
	Reply string `json:"reply,omitempty"`
	Error string `json:"error,omitempty"`
	Image string `json:"image,omitempty"` // data URL for screenshot/image
}

func (c *Channel) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow any origin for local development.
	})
	if err != nil {
		log.Printf("[wschannel] accept error: %v", err)
		return
	}
	defer conn.CloseNow()

	// Generate session ID.
	sessionID := "ws-" + r.RemoteAddr

	c.mu.Lock()
	c.conns[sessionID] = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.conns, sessionID)
		c.mu.Unlock()
	}()

	log.Printf("[wschannel] client connected: %s", sessionID)

	ctx := r.Context()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				log.Printf("[wschannel] client disconnected: %s", sessionID)
			} else {
				log.Printf("[wschannel] read error: %v", err)
			}
			return
		}

		var incoming wsIncoming
		if err := json.Unmarshal(data, &incoming); err != nil {
			c.sendError(ctx, conn, "invalid JSON")
			continue
		}

		if incoming.SessionID != "" && incoming.SessionID != sessionID {
			c.mu.Lock()
			delete(c.conns, sessionID)
			sessionID = incoming.SessionID
			c.conns[sessionID] = conn
			c.mu.Unlock()
		}

		reply, err := c.cfg.OnMessage(ctx, sessionID, incoming.Message)
		if err != nil {
			c.sendError(ctx, conn, err.Error())
			continue
		}

		msg := wsMessage{Reply: reply}
		respData, _ := json.Marshal(msg)
		if err := conn.Write(ctx, websocket.MessageText, respData); err != nil {
			log.Printf("[wschannel] write error: %v", err)
			return
		}
	}
}

func (c *Channel) sendError(ctx context.Context, conn *websocket.Conn, msg string) {
	data, _ := json.Marshal(wsMessage{Error: msg})
	conn.Write(ctx, websocket.MessageText, data)
}
