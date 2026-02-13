// Package provider defines the interface for LLM providers.
// Every backend (Copilot, Google AI Studio, Ollama) implements this interface,
// allowing the router and scheduler to treat them uniformly.
package provider

import "context"

// Provider is the core abstraction for LLM backends.
// Chat is for one-shot calls; Stream is for interactive output.
type Provider interface {
	// Chat sends a request and returns the complete response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Stream sends a request and returns a receive-only channel of events.
	// The channel is closed when the stream completes (final event has Done=true).
	// Callers should range over the channel and check each event's Error field.
	Stream(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error)

	// Models returns the list of models available from this provider.
	Models() []ModelInfo

	// Name returns the provider name (e.g. "copilot", "google", "ollama").
	Name() string
}

// ChatRequest is the input to both Chat and Stream.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Message represents a single message in a conversation.
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"`
}

// ChatResponse is the complete response from a Chat call.
type ChatResponse struct {
	Content string `json:"content"`
	Usage   Usage  `json:"usage"`
	Model   string `json:"model"`
}

// StreamEvent is a single chunk from a streaming response.
// Error is non-nil if the stream encountered a problem.
// Usage is non-nil only on the final event (Done=true).
type StreamEvent struct {
	Delta string // text chunk (empty on final event)
	Done  bool   // true on the last event
	Usage *Usage // non-nil only on final event
	Error error  // non-nil if stream failed
}

// Usage contains token usage statistics returned by the API.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
}

// ModelInfo describes a model available from a provider.
type ModelInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Premium  bool   `json:"premium"`  // true if quota-limited
	Provider string `json:"provider"` // provider name
}
