// Package copilot implements the provider.Provider interface for GitHub Copilot.
//
// It calls the undocumented api.githubcopilot.com/chat/completions endpoint,
// which is OpenAI-compatible. Authentication is handled by auth.TokenManager.
package copilot

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

	"github.com/Ask149/aidaemon/internal/auth"
	"github.com/Ask149/aidaemon/internal/provider"
)

const (
	completionsURL = "https://api.githubcopilot.com/chat/completions"
)

// Required headers that identify us as a Copilot integration.
// Values sourced from OpenCode and Crush source code.
var copilotHeaders = map[string]string{
	"Editor-Version":        "vscode/1.105.1",
	"Editor-Plugin-Version": "copilot-chat/0.32.4",
	"Copilot-Integration-Id": "vscode-chat",
	"Openai-Intent":         "conversation-panel",
	"Content-Type":          "application/json",
}

// Available models — verified against the Copilot API on Feb 13, 2026.
// Model IDs that return 400 "not supported" are excluded.
var models = []provider.ModelInfo{
	// Base tier (unlimited on Copilot Individual).
	{ID: "gpt-4o", Name: "GPT-4o", Premium: false, Provider: "copilot"},
	{ID: "gpt-4.1", Name: "GPT-4.1", Premium: false, Provider: "copilot"},
	{ID: "gpt-4o-mini", Name: "GPT-4o Mini", Premium: false, Provider: "copilot"},

	// Premium tier (~300 req/month on Copilot Individual).
	{ID: "claude-sonnet-4", Name: "Claude Sonnet 4", Premium: true, Provider: "copilot"},
	{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Premium: true, Provider: "copilot"},
}

// Provider implements provider.Provider for GitHub Copilot.
type Provider struct {
	tokenManager *auth.TokenManager
	client       *http.Client
}

// New creates a new Copilot provider.
func New(tm *auth.TokenManager) *Provider {
	return &Provider{
		tokenManager: tm,
		client: &http.Client{
			Timeout: 120 * time.Second, // LLM responses can be slow
		},
	}
}

func (p *Provider) Name() string {
	return "copilot"
}

func (p *Provider) Models() []provider.ModelInfo {
	return models
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
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Message.Content
	}

	return &provider.ChatResponse{
		Content: content,
		Model:   completion.Model,
		Usage: provider.Usage{
			InputTokens:  completion.Usage.PromptTokens,
			OutputTokens: completion.Usage.CompletionTokens,
			CachedTokens: completion.Usage.PromptTokensDetails.CachedTokens,
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

// buildRequestBody creates the JSON request body.
func (p *Provider) buildRequestBody(req provider.ChatRequest, stream bool) []byte {
	messages := make([]map[string]string, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = map[string]string{
			"role":    m.Role,
			"content": m.Content,
		}
	}

	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   stream,
	}

	if stream {
		payload["stream_options"] = map[string]bool{
			"include_usage": true,
		}
	}

	data, _ := json.Marshal(payload)
	return data
}

// doRequest performs the HTTP POST with auth, including retry-on-401.
func (p *Provider) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	tok, err := p.tokenManager.GetToken()
	if err != nil {
		return nil, fmt.Errorf("get copilot token: %w", err)
	}

	resp, err := p.sendRequest(ctx, tok.Token, body)
	if err != nil {
		return nil, err
	}

	// Retry once on 401 (token may have expired mid-request).
	if resp.StatusCode == 401 {
		resp.Body.Close()

		tok, err = p.tokenManager.ForceRefresh()
		if err != nil {
			return nil, fmt.Errorf("token refresh after 401: %w", err)
		}

		resp, err = p.sendRequest(ctx, tok.Token, body)
		if err != nil {
			return nil, err
		}
	}

	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("copilot API error: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	return resp.Body, nil
}

// sendRequest performs the raw HTTP POST.
func (p *Provider) sendRequest(ctx context.Context, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", completionsURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range copilotHeaders {
		req.Header.Set(k, v)
	}

	return p.client.Do(req)
}

// readSSE reads the SSE stream and sends events to the channel.
// It closes the channel and the response body when done.
func (p *Provider) readSSE(body io.ReadCloser, ch chan<- provider.StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var usage *provider.Usage

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {json}" or "data: [DONE]"
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

		// Extract text delta.
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			ch <- provider.StreamEvent{
				Delta: chunk.Choices[0].Delta.Content,
			}
		}

		// Capture usage from the final chunk (stream_options.include_usage).
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = &provider.Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				CachedTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
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
	Model   string           `json:"model"`
	Choices []openAIChoice   `json:"choices"`
	Usage   openAIUsage      `json:"usage"`
}

type openAIChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
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
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}
