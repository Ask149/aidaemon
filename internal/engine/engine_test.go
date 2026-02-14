package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/testutil"
	"github.com/Ask149/aidaemon/internal/tools"
)

// helpers -------------------------------------------------------

func registry(tt ...*testutil.DummyTool) *tools.Registry {
	r := tools.NewRegistry(nil) // no permissions
	for _, t := range tt {
		r.Register(t)
	}
	return r
}

func msgs(content string) []provider.Message {
	return []provider.Message{
		{Role: "user", Content: content},
	}
}

// tests ---------------------------------------------------------

func TestRun_SimpleTextResponse(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatResponse: &provider.ChatResponse{
			Content:      "Hello world",
			FinishReason: "stop",
			Usage:        provider.Usage{InputTokens: 10, OutputTokens: 5},
		},
	}

	e := &Engine{Provider: mp}
	result, err := e.Run(context.Background(), msgs("hi"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Hello world" {
		t.Errorf("content = %q, want %q", result.Content, "Hello world")
	}
	if result.ToolIterations != 0 {
		t.Errorf("tool iterations = %d, want 0", result.ToolIterations)
	}
	if result.Usage == nil {
		t.Fatal("usage is nil")
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("input tokens = %d, want 10", result.Usage.InputTokens)
	}
	if len(mp.ChatCalls) != 1 {
		t.Errorf("chat calls = %d, want 1", len(mp.ChatCalls))
	}
	if mp.ChatCalls[0].Model != "test" {
		t.Errorf("model = %q, want %q", mp.ChatCalls[0].Model, "test")
	}
}

func TestRun_EmptyResponse(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatResponse: &provider.ChatResponse{
			Content:      "",
			FinishReason: "stop",
		},
	}

	e := &Engine{Provider: mp}
	result, err := e.Run(context.Background(), msgs("hi"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("content = %q, want empty", result.Content)
	}
}

func TestRun_LLMError(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatFn: func(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
			return nil, errors.New("network error")
		},
	}

	e := &Engine{Provider: mp}
	_, err := e.Run(context.Background(), msgs("hi"), RunOptions{Model: "test"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "LLM error: network error" {
		t.Errorf("error = %q, want %q", got, "LLM error: network error")
	}
}

func TestRun_SingleToolCall(t *testing.T) {
	callCount := 0
	mp := &testutil.MockProvider{
		ChatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: request a tool call
				return &provider.ChatResponse{
					ToolCalls: []provider.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: provider.FuncCall{
								Name:      "echo",
								Arguments: `{"text":"hello"}`,
							},
						},
					},
				}, nil
			}
			// Second call: return final text
			return &provider.ChatResponse{
				Content:      "Tool said: hello",
				FinishReason: "stop",
				Usage:        provider.Usage{InputTokens: 50, OutputTokens: 10},
			}, nil
		},
	}

	dummy := &testutil.DummyTool{
		ToolName: "echo",
		Result:   "hello",
	}
	reg := registry(dummy)

	e := &Engine{Provider: mp, Registry: reg}
	result, err := e.Run(context.Background(), msgs("say hello"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Tool said: hello" {
		t.Errorf("content = %q, want %q", result.Content, "Tool said: hello")
	}
	if result.ToolIterations != 1 {
		t.Errorf("tool iterations = %d, want 1", result.ToolIterations)
	}
	if len(result.ToolNames) != 1 || result.ToolNames[0] != "echo" {
		t.Errorf("tool names = %v, want [echo]", result.ToolNames)
	}
	if len(dummy.ExecuteCalls) != 1 {
		t.Errorf("execute calls = %d, want 1", len(dummy.ExecuteCalls))
	}
}

func TestRun_MultiIterationToolChain(t *testing.T) {
	callCount := 0
	mp := &testutil.MockProvider{
		ChatFn: func(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
			callCount++
			if callCount <= 3 {
				return &provider.ChatResponse{
					ToolCalls: []provider.ToolCall{
						{
							ID:   fmt.Sprintf("call_%d", callCount),
							Type: "function",
							Function: provider.FuncCall{
								Name:      "step",
								Arguments: fmt.Sprintf(`{"n":%d}`, callCount),
							},
						},
					},
				}, nil
			}
			return &provider.ChatResponse{
				Content:      "Done after 3 steps",
				FinishReason: "stop",
				Usage:        provider.Usage{InputTokens: 100, OutputTokens: 20},
			}, nil
		},
	}

	dummy := &testutil.DummyTool{
		ToolName: "step",
		Result:   "ok",
	}
	reg := registry(dummy)

	e := &Engine{Provider: mp, Registry: reg}
	result, err := e.Run(context.Background(), msgs("multi step"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ToolIterations != 3 {
		t.Errorf("tool iterations = %d, want 3", result.ToolIterations)
	}
	if len(dummy.ExecuteCalls) != 3 {
		t.Errorf("execute calls = %d, want 3", len(dummy.ExecuteCalls))
	}
	if result.Content != "Done after 3 steps" {
		t.Errorf("content = %q, want %q", result.Content, "Done after 3 steps")
	}
}

func TestRun_MaxIterationsSummary(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatFn: func(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
			// If the request has no tools, we're in the summary request
			if len(req.Tools) == 0 {
				return &provider.ChatResponse{
					Content:      "Summary: ran out of iterations",
					FinishReason: "stop",
					Usage:        provider.Usage{InputTokens: 200, OutputTokens: 30},
				}, nil
			}
			// Always return a tool call (will hit limit)
			return &provider.ChatResponse{
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_loop",
						Type: "function",
						Function: provider.FuncCall{
							Name:      "loop",
							Arguments: "{}",
						},
					},
				},
			}, nil
		},
	}

	dummy := &testutil.DummyTool{
		ToolName: "loop",
		Result:   "looping",
	}
	reg := registry(dummy)

	e := &Engine{Provider: mp, Registry: reg}
	result, err := e.Run(context.Background(), msgs("loop forever"), RunOptions{
		Model:         "test",
		MaxIterations: 3,
	})

	// The engine returns an error AND a partial result (the summary).
	// Wait — looking at the code, if summary succeeds, it returns result, nil.
	// Actually, looking at the code more carefully:
	// After the max-iteration summary, it returns result, nil if summary succeeded.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Summary: ran out of iterations" {
		t.Errorf("content = %q, want summary", result.Content)
	}
	if result.ToolIterations != 3 {
		t.Errorf("tool iterations = %d, want 3", result.ToolIterations)
	}
	if len(dummy.ExecuteCalls) != 3 {
		t.Errorf("execute calls = %d, want 3", len(dummy.ExecuteCalls))
	}
}

func TestRun_DoesNotMutateInput(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatResponse: &provider.ChatResponse{
			Content:      "ok",
			FinishReason: "stop",
		},
	}

	input := msgs("original")
	originalLen := len(input)

	e := &Engine{Provider: mp}
	_, err := e.Run(context.Background(), input, RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(input) != originalLen {
		t.Errorf("input was mutated: len=%d, want %d", len(input), originalLen)
	}
	if input[0].Content != "original" {
		t.Errorf("input[0].Content = %q, want %q", input[0].Content, "original")
	}
}

func TestRun_ToolDefsFromRegistry(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatResponse: &provider.ChatResponse{
			Content:      "ok",
			FinishReason: "stop",
		},
	}

	dummy := &testutil.DummyTool{
		ToolName: "my_tool",
		ToolDesc: "My awesome tool",
		ToolParams: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"x": map[string]interface{}{"type": "string"},
			},
		},
	}
	reg := registry(dummy)

	e := &Engine{Provider: mp, Registry: reg}
	_, err := e.Run(context.Background(), msgs("hi"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the request included tool definitions
	if len(mp.ChatCalls) != 1 {
		t.Fatalf("chat calls = %d, want 1", len(mp.ChatCalls))
	}
	req := mp.ChatCalls[0]
	if len(req.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "my_tool" {
		t.Errorf("tool name = %q, want %q", req.Tools[0].Function.Name, "my_tool")
	}
	if req.Tools[0].Function.Description != "My awesome tool" {
		t.Errorf("tool desc = %q, want %q", req.Tools[0].Function.Description, "My awesome tool")
	}
}

func TestRun_NoRegistryNoTools(t *testing.T) {
	mp := &testutil.MockProvider{
		ChatResponse: &provider.ChatResponse{
			Content:      "no tools",
			FinishReason: "stop",
		},
	}

	// Engine with no registry
	e := &Engine{Provider: mp}
	_, err := e.Run(context.Background(), msgs("hi"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := mp.ChatCalls[0]
	if len(req.Tools) != 0 {
		t.Errorf("tools = %d, want 0", len(req.Tools))
	}
}

func TestRun_CustomExecutor(t *testing.T) {
	callCount := 0
	mp := &testutil.MockProvider{
		ChatFn: func(_ context.Context, _ provider.ChatRequest) (*provider.ChatResponse, error) {
			callCount++
			if callCount == 1 {
				return &provider.ChatResponse{
					ToolCalls: []provider.ToolCall{
						{
							ID:   "call_x",
							Type: "function",
							Function: provider.FuncCall{
								Name:      "custom",
								Arguments: "{}",
							},
						},
					},
				}, nil
			}
			return &provider.ChatResponse{
				Content:      "used custom executor",
				FinishReason: "stop",
			}, nil
		},
	}

	var executedCalls []provider.ToolCall
	customExec := &mockExecutor{
		fn: func(ctx context.Context, calls []provider.ToolCall) []provider.Message {
			executedCalls = calls
			results := make([]provider.Message, len(calls))
			for i, c := range calls {
				results[i] = provider.Message{
					Role:       "tool",
					Content:    "custom result",
					ToolCallID: c.ID,
				}
			}
			return results
		},
	}

	e := &Engine{Provider: mp, Executor: customExec}
	result, err := e.Run(context.Background(), msgs("custom"), RunOptions{Model: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "used custom executor" {
		t.Errorf("content = %q, want %q", result.Content, "used custom executor")
	}
	if len(executedCalls) != 1 {
		t.Fatalf("executed calls = %d, want 1", len(executedCalls))
	}
	if executedCalls[0].Function.Name != "custom" {
		t.Errorf("call name = %q, want %q", executedCalls[0].Function.Name, "custom")
	}
}

func TestDefaultExecutor_ToolError(t *testing.T) {
	dummy := &testutil.DummyTool{
		ToolName: "fail",
		ExecuteFn: func(_ context.Context, _ map[string]interface{}) (string, error) {
			return "", errors.New("tool broke")
		},
	}
	reg := registry(dummy)

	exec := &DefaultExecutor{Registry: reg}
	calls := []provider.ToolCall{
		{
			ID:   "call_fail",
			Type: "function",
			Function: provider.FuncCall{
				Name:      "fail",
				Arguments: "{}",
			},
		},
	}

	results := exec.ExecuteToolCalls(context.Background(), calls)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	// The DefaultExecutor wraps errors in "Error: ..." format
	if results[0].Content != "Error: tool execution failed: tool broke" {
		t.Errorf("content = %q", results[0].Content)
	}
	if results[0].ToolCallID != "call_fail" {
		t.Errorf("tool call id = %q, want %q", results[0].ToolCallID, "call_fail")
	}
}

func TestToolNamesSummary(t *testing.T) {
	r := &Result{ToolNames: []string{"read_file", "write_file", "run_command"}}
	got := r.ToolNamesSummary()
	want := "read_file, write_file, run_command"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}

	empty := &Result{}
	if s := empty.ToolNamesSummary(); s != "" {
		t.Errorf("empty summary = %q, want empty", s)
	}
}

// mockExecutor is a test helper implementing ToolExecutor.
type mockExecutor struct {
	fn func(ctx context.Context, calls []provider.ToolCall) []provider.Message
}

func (m *mockExecutor) ExecuteToolCalls(ctx context.Context, calls []provider.ToolCall) []provider.Message {
	return m.fn(ctx, calls)
}
