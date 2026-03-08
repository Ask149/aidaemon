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
	"log"
	"net/http"
	"strings"
	"sync"
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

// fallbackModels is the hardcoded list used when the /models API is unreachable.
// Verified against the Copilot API on Feb 13, 2026.
var fallbackModels = []provider.ModelInfo{
	// Base tier (unlimited).
	{ID: "gpt-4o", Name: "GPT-4o", Provider: "copilot", Vendor: "OpenAI"},
	{ID: "gpt-4.1", Name: "GPT-4.1", Provider: "copilot", Vendor: "OpenAI"},
	{ID: "gpt-4o-mini", Name: "GPT-4o Mini", Provider: "copilot", Vendor: "OpenAI"},
	// Premium tier.
	{ID: "gpt-5", Name: "GPT-5", Premium: true, Provider: "copilot", Vendor: "OpenAI"},
	{ID: "gpt-5-mini", Name: "GPT-5 Mini", Premium: true, Provider: "copilot", Vendor: "OpenAI"},
	{ID: "gpt-5.1", Name: "GPT-5.1", Premium: true, Provider: "copilot", Vendor: "OpenAI"},
	{ID: "gpt-5.2", Name: "GPT-5.2", Premium: true, Provider: "copilot", Vendor: "OpenAI"},
	{ID: "claude-sonnet-4", Name: "Claude Sonnet 4", Premium: true, Provider: "copilot", Vendor: "Anthropic"},
	{ID: "claude-sonnet-4.5", Name: "Claude Sonnet 4.5", Premium: true, Provider: "copilot", Vendor: "Anthropic"},
	{ID: "claude-opus-4.5", Name: "Claude Opus 4.5", Premium: true, Provider: "copilot", Vendor: "Anthropic"},
	{ID: "claude-opus-4.6", Name: "Claude Opus 4.6", Premium: true, Provider: "copilot", Vendor: "Anthropic"},
	{ID: "claude-opus-4.6-fast", Name: "Claude Opus 4.6 Fast", Premium: true, Provider: "copilot", Vendor: "Anthropic"},
	{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5", Premium: true, Provider: "copilot", Vendor: "Anthropic"},
	{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", Premium: true, Provider: "copilot", Vendor: "Google"},
	{ID: "gemini-3-pro-preview", Name: "Gemini 3 Pro Preview", Premium: true, Provider: "copilot", Vendor: "Google"},
	{ID: "gemini-3-flash-preview", Name: "Gemini 3 Flash Preview", Premium: true, Provider: "copilot", Vendor: "Google"},
}

const modelCacheTTL = 1 * time.Hour

// Provider implements provider.Provider for GitHub Copilot.
type Provider struct {
	tokenManager *auth.TokenManager
	client       *http.Client

	// Dynamic model cache.
	modelsMu      sync.Mutex
	cachedModels  []provider.ModelInfo
	modelsFetched time.Time
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
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()

	// Return cached if fresh.
	if p.cachedModels != nil && time.Since(p.modelsFetched) < modelCacheTTL {
		return p.cachedModels
	}

	// Fetch from API.
	entries, err := p.tokenManager.FetchModels()
	if err != nil {
		log.Printf("[copilot] model discovery failed, using fallback: %v", err)
		if p.cachedModels != nil {
			return p.cachedModels // stale cache better than nothing
		}
		return fallbackModels
	}

	// Map API entries → ModelInfo.
	models := make([]provider.ModelInfo, 0, len(entries))
	for _, e := range entries {
		models = append(models, provider.ModelInfo{
			ID:               e.ID,
			Name:             e.Name,
			Premium:          e.Billing.IsPremium,
			Provider:         "copilot",
			Vendor:           e.Vendor,
			MaxContextTokens: e.Capabilities.Limits.MaxContextWindowTokens,
			MaxOutputTokens:  e.Capabilities.Limits.MaxOutputTokens,
		})
	}

	p.cachedModels = models
	p.modelsFetched = time.Now()
	log.Printf("[copilot] discovered %d models from API", len(models))
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

	// Debug: log raw response to diagnose tool_calls parsing issues.
	log.Printf("[copilot] raw response (%d bytes): %s", len(data), truncate(string(data), 1000))

	var completion openAIResponse
	if err := json.Unmarshal(data, &completion); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %s)", err, truncate(string(data), 500))
	}

	content := ""
	var toolCalls []provider.ToolCall
	finishReason := ""

	// Scan ALL choices — the Copilot API sometimes splits content and tool_calls
	// across multiple choices (e.g. choices[0] has text, choices[1] has tool_calls).
	for i, choice := range completion.Choices {
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}
		if choice.Message.Content != "" {
			if content != "" {
				content += "\n" // Join content from multiple choices.
			}
			content += choice.Message.Content
		}

		log.Printf("[copilot] choice[%d]: finish_reason=%s, content_len=%d, tool_calls=%d",
			i, choice.FinishReason, len(choice.Message.Content), len(choice.Message.ToolCalls))

		// Collect tool calls from any choice.
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

	log.Printf("[copilot] final: finish_reason=%s, content_len=%d, total_tool_calls=%d",
		finishReason, len(content), len(toolCalls))

	return &provider.ChatResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Model:        completion.Model,
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
	messages := make([]map[string]interface{}, len(req.Messages))
	for i, m := range req.Messages {
		msg := map[string]interface{}{
			"role": m.Role,
		}

		// Add content: multi-modal (ContentParts) takes priority over plain string.
		if len(m.ContentParts) > 0 {
			// OpenAI format: content is an array of {type, text} or {type, image_url} objects.
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

		// Add tool_calls if present (assistant messages).
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}

		// Add tool_call_id if present (tool messages).
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

	// Add tools if provided.
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

// truncate shortens a string for logging.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}

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
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}
