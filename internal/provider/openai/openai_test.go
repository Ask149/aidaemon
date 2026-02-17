package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ask149/aidaemon/internal/provider"
)

func TestName(t *testing.T) {
	p := New(Config{})
	if got := p.Name(); got != "openai" {
		t.Errorf("Name() = %q, want %q", got, "openai")
	}
}

func TestModels(t *testing.T) {
	p := New(Config{Model: "gpt-4o"})
	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Errorf("model ID = %q, want %q", models[0].ID, "gpt-4o")
	}
}

func TestModels_Empty(t *testing.T) {
	p := New(Config{})
	if models := p.Models(); len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

func TestChat_BasicResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		json.NewEncoder(w).Encode(openAIResponse{
			Model: "gpt-4o",
			Choices: []openAIChoice{{
				Message:      openAIMessage{Content: "Hello!"},
				FinishReason: "stop",
			}},
			Usage: openAIUsage{PromptTokens: 10, CompletionTokens: 5},
		})
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello!")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Usage.InputTokens)
	}
}

func TestChat_ToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIResponse{
			Model: "gpt-4o",
			Choices: []openAIChoice{{
				Message: openAIMessage{
					ToolCalls: []openAIToolCall{{
						ID:   "call_123",
						Type: "function",
						Function: openAIFunction{
							Name:      "web_search",
							Arguments: `{"query":"test"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: "user", Content: "search"}},
	})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_123" {
		t.Errorf("ToolCall ID = %q, want %q", tc.ID, "call_123")
	}
	if tc.Function.Name != "web_search" {
		t.Errorf("ToolCall name = %q, want %q", tc.Function.Name, "web_search")
	}
}

func TestChat_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"invalid model"}}`))
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})
	_, err := p.Chat(context.Background(), provider.ChatRequest{
		Model:    "bad-model",
		Messages: []provider.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should contain status code: %v", err)
	}
}

func TestChat_AzureAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("api-key"); got != "azure-key" {
			t.Errorf("api-key header = %q, want %q", got, "azure-key")
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization should be empty for Azure, got %q", got)
		}
		if got := r.URL.Query().Get("api-version"); got != "2024-02-01" {
			t.Errorf("api-version = %q, want %q", got, "2024-02-01")
		}
		json.NewEncoder(w).Encode(openAIResponse{
			Model:   "gpt-4o",
			Choices: []openAIChoice{{Message: openAIMessage{Content: "OK"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	p := New(Config{
		BaseURL:         srv.URL,
		APIKey:          "azure-key",
		AzureAPIVersion: "2024-02-01",
	})
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}
}

func TestStream_BasicResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":" world"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, APIKey: "test-key"})
	ch, err := p.Stream(context.Background(), provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var text string
	var finalUsage *provider.Usage
	for ev := range ch {
		if ev.Error != nil {
			t.Fatalf("stream error: %v", ev.Error)
		}
		text += ev.Delta
		if ev.Done {
			finalUsage = ev.Usage
		}
	}

	if text != "Hello world" {
		t.Errorf("streamed text = %q, want %q", text, "Hello world")
	}
	if finalUsage == nil {
		t.Fatal("expected usage on final event")
	}
	if finalUsage.InputTokens != 5 {
		t.Errorf("InputTokens = %d, want 5", finalUsage.InputTokens)
	}
}
