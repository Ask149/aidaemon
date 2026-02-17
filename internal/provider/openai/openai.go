// Package openai implements the provider.Provider interface for OpenAI-compatible APIs.
//
// It supports standard OpenAI endpoints as well as Azure OpenAI (via the
// AzureAPIVersion config field, which switches authentication from
// Authorization: Bearer to the api-key header).
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/provider"
)

// Config holds the configuration for an OpenAI-compatible provider.
type Config struct {
	BaseURL         string // e.g. "https://api.openai.com/v1"
	APIKey          string
	AzureAPIVersion string // non-empty activates Azure auth mode
	Model           string // single model ID to expose
}

// Provider implements provider.Provider for OpenAI-compatible APIs.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New creates a new OpenAI provider.
func New(cfg Config) *Provider {
	return &Provider{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Name returns "openai".
func (p *Provider) Name() string {
	return "openai"
}

// Models returns a single-element list if a model is configured, or empty.
func (p *Provider) Models() []provider.ModelInfo {
	if p.cfg.Model == "" {
		return nil
	}
	return []provider.ModelInfo{
		{
			ID:       p.cfg.Model,
			Name:     p.cfg.Model,
			Provider: "openai",
		},
	}
}

// Chat sends a non-streaming request and returns the complete response.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	body := p.buildRequestBody(req, false)

	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	data, err := io.ReadAll(respBody)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var completion openAIResponse
	if err := json.Unmarshal(data, &completion); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	content := ""
	var toolCalls []provider.ToolCall
	finishReason := ""

	for _, choice := range completion.Choices {
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
		if choice.Message.Content != "" {
			if content != "" {
				content += "\n"
			}
			content += choice.Message.Content
		}
		for _, tc := range choice.Message.ToolCalls {
			toolCalls = append(toolCalls, provider.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: provider.FuncCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}

	return &provider.ChatResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Model:        completion.Model,
		Usage: provider.Usage{
			InputTokens:  completion.Usage.PromptTokens,
			OutputTokens: completion.Usage.CompletionTokens,
		},
	}, nil
}

// Stream sends a streaming request and returns a channel of events.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	body := p.buildRequestBody(req, true)

	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan provider.StreamEvent, 32)
	go p.readSSE(respBody, ch)
	return ch, nil
}

// endpoint builds the API URL: {BaseURL}/chat/completions with optional ?api-version=.
func (p *Provider) endpoint() string {
	url := strings.TrimRight(p.cfg.BaseURL, "/") + "/chat/completions"
	if p.cfg.AzureAPIVersion != "" {
		url += "?api-version=" + p.cfg.AzureAPIVersion
	}
	return url
}

// doRequest performs the HTTP POST with appropriate auth headers.
func (p *Provider) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Azure mode: use api-key header; standard mode: use Bearer token.
	if p.cfg.AzureAPIVersion != "" {
		req.Header.Set("api-key", p.cfg.APIKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai API error: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	return resp.Body, nil
}

// buildRequestBody creates the JSON request body in OpenAI format.
func (p *Provider) buildRequestBody(req provider.ChatRequest, stream bool) []byte {
	messages := make([]map[string]interface{}, len(req.Messages))
	for i, m := range req.Messages {
		msg := map[string]interface{}{
			"role": m.Role,
		}

		if len(m.ContentParts) > 0 {
			parts := make([]map[string]interface{}, len(m.ContentParts))
			for j, part := range m.ContentParts {
				p := map[string]interface{}{"type": part.Type}
				if part.Type == "text" {
					p["text"] = part.Text
				} else if part.Type == "image_url" && part.ImageURL != nil {
					p["image_url"] = map[string]string{"url": part.ImageURL.URL}
				}
				parts[j] = p
			}
			msg["content"] = parts
		} else if m.Content != "" {
			msg["content"] = m.Content
		}

		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}

		messages[i] = msg
	}

	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   stream,
	}

	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}

	if stream {
		payload["stream_options"] = map[string]bool{
			"include_usage": true,
		}
	}

	data, _ := json.Marshal(payload)
	return data
}

// readSSE reads the SSE stream and sends events to the channel.
func (p *Provider) readSSE(body io.ReadCloser, ch chan<- provider.StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var usage *provider.Usage

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			ch <- provider.StreamEvent{
				Done:  true,
				Usage: usage,
			}
			return
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- provider.StreamEvent{Error: fmt.Errorf("parse SSE chunk: %w", err)}
			return
		}

		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			ch <- provider.StreamEvent{
				Delta: chunk.Choices[0].Delta.Content,
			}
		}

		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = &provider.Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- provider.StreamEvent{Error: fmt.Errorf("read SSE stream: %w", err)}
		return
	}

	// Stream ended without [DONE] — still send final event.
	ch <- provider.StreamEvent{
		Done:  true,
		Usage: usage,
	}
}

// --- OpenAI-compatible response types ---

type openAIResponse struct {
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIMessage struct {
	Content   string           `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIStreamChunk struct {
	Choices []openAIStreamChoice `json:"choices"`
	Usage   openAIUsage          `json:"usage"`
}

type openAIStreamChoice struct {
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
}

type openAIUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}
