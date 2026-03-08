// Package channel defines the transport adapter interface.
//
// Each message transport (Telegram, WebSocket, CLI) implements the
// Channel interface, allowing the engine and heartbeat to send
// messages without knowing which transport is in use.
package channel

import "context"

// Channel is the interface for message transport adapters.
// Each channel (Telegram, WebSocket, CLI) implements this.
type Channel interface {
	// Name returns the channel identifier (e.g., "telegram", "websocket").
	Name() string

	// Start begins listening for messages. Blocks until ctx is cancelled.
	Start(ctx context.Context) error

	// Send delivers a message to a specific session.
	// Used by heartbeat and other server-initiated messages.
	Send(ctx context.Context, sessionID string, text string) error
}

// SessionID creates a canonical session identifier.
// Format: "<channel>:<chatId>"
func SessionID(channel, chatID string) string {
	return channel + ":" + chatID
}
