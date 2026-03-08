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
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []ToolDef      `json:"tools,omitempty"`
}

// ToolDef is the OpenAI function calling format.
type ToolDef struct {
	Type     string   `json:"type"`
	Function FuncDef  `json:"function"`
}

// FuncDef describes a callable function.
type FuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Message represents a single message in a conversation.
type Message struct {
	Role         string        `json:"role"`                   // "system", "user", "assistant", "tool"
	Content      string        `json:"content,omitempty"`
	ContentParts []ContentPart `json:"content_parts,omitempty"` // Multi-modal (text + images)
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`    // For assistant messages
	ToolCallID   string        `json:"tool_call_id,omitempty"`  // For tool messages
}

// ContentPart is a single part of a multi-modal message.
type ContentPart struct {
	Type     string    `json:"type"`                // "text" or "image_url"
	Text     string    `json:"text,omitempty"`      // For type="text"
	ImageURL *ImageURL `json:"image_url,omitempty"` // For type="image_url"
}

// ImageURL contains the image data or URL.
type ImageURL struct {
	URL string `json:"url"` // "data:image/jpeg;base64,..." or https URL
}

// ToolCall represents a tool invocation from the LLM.
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function FuncCall `json:"function"`
}

// FuncCall contains the function name and arguments.
type FuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatResponse is the complete response from a Chat call.
type ChatResponse struct {
	Content      string     `json:"content,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Usage        Usage      `json:"usage"`
	Model        string     `json:"model"`
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
	ID               string `json:"id"`
	Name             string `json:"name"`
	Premium          bool   `json:"premium"`            // true if quota-limited
	Provider         string `json:"provider"`           // provider name
	Vendor           string `json:"vendor,omitempty"`   // e.g. "OpenAI", "Anthropic"
	MaxContextTokens int    `json:"max_context_tokens"` // 0 = unknown
	MaxOutputTokens  int    `json:"max_output_tokens"`  // 0 = unknown
}
