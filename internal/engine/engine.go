// Package engine provides the core LLM chat orchestration loop.
//
// It handles the tool-call iteration pattern: send messages to the LLM,
// execute any tool calls, feed results back, and repeat until the LLM
// produces a final text response (or the iteration limit is reached).
//
// Both the Telegram bot and HTTP API delegate to this engine,
// eliminating duplicated orchestration logic.
package engine

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/tools"
)

// ToolExecutor handles tool call execution. This allows the Telegram
// bot to inject its own executor that handles screenshots, MCP images,
// etc., while the HTTP API uses a simpler executor.
type ToolExecutor interface {
	// ExecuteToolCalls runs tool calls and returns provider.Messages
	// with role="tool" and proper ToolCallID fields.
	ExecuteToolCalls(ctx context.Context, calls []provider.ToolCall) []provider.Message
}

// DefaultExecutor executes tools via the registry without any
// platform-specific handling (no Telegram screenshots, etc.).
type DefaultExecutor struct {
	Registry *tools.Registry
}

// ExecuteToolCalls implements ToolExecutor using the registry.
func (e *DefaultExecutor) ExecuteToolCalls(ctx context.Context, calls []provider.ToolCall) []provider.Message {
	results := make([]provider.Message, len(calls))
	for i, call := range calls {
		result, err := e.Registry.Execute(ctx, call.Function.Name, call.Function.Arguments)
		content := result
		if err != nil {
			content = fmt.Sprintf("Error: %v", err)
		}
		results[i] = provider.Message{
			Role:       "tool",
			Content:    content,
			ToolCallID: call.ID,
		}
	}
	return results
}

// Engine orchestrates the LLM ↔ tool-call loop.
type Engine struct {
	Provider provider.Provider
	Registry *tools.Registry
	Executor ToolExecutor // if nil, uses DefaultExecutor
}

// RunOptions configures a single engine run.
type RunOptions struct {
	Model         string
	MaxIterations int // 0 = default (999)
}

// Result contains everything produced by a single engine run.
type Result struct {
	// Content is the final text response from the LLM.
	Content string

	// Usage is the token usage from the final LLM call.
	Usage *provider.Usage

	// ToolIterations is how many tool-call iterations occurred (0 = none).
	ToolIterations int

	// Messages is the full in-memory message list after the run,
	// including all tool_call and tool result messages.
	Messages []provider.Message

	// ToolNames lists all tool names that were called during the run.
	ToolNames []string
}

// Run executes the chat loop: send messages to LLM, execute tools,
// repeat until a final text response or max iterations.
//
// The provided messages slice is NOT modified; a copy is used internally.
// If the LLM hits the iteration limit, a summary request is made.
func (e *Engine) Run(ctx context.Context, messages []provider.Message, opts RunOptions) (*Result, error) {
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = 999
	}

	executor := e.Executor
	if executor == nil {
		executor = &DefaultExecutor{Registry: e.Registry}
	}

	// Copy messages to avoid mutating the caller's slice.
	msgs := make([]provider.Message, len(messages))
	copy(msgs, messages)

	// Build tool definitions.
	var toolDefs []provider.ToolDef
	if e.Registry != nil {
		for _, t := range e.Registry.List() {
			toolDefs = append(toolDefs, provider.ToolDef{
				Type: "function",
				Function: provider.FuncDef{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  t.Parameters(),
				},
			})
		}
	}

	result := &Result{}

	for i := 0; i < maxIter; i++ {
		req := provider.ChatRequest{
			Model:    opts.Model,
			Messages: msgs,
			Tools:    toolDefs,
		}

		log.Printf("[engine] calling LLM (iteration %d, %d messages)", i+1, len(msgs))
		resp, err := e.Provider.Chat(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM error: %w", err)
		}
		log.Printf("[engine] LLM response: finish_reason=%s, tool_calls=%d, content_len=%d",
			resp.FinishReason, len(resp.ToolCalls), len(resp.Content))

		// Check if LLM wants to call tools.
		if len(resp.ToolCalls) > 0 {
			result.ToolIterations++

			// Record tool names.
			for _, tc := range resp.ToolCalls {
				result.ToolNames = append(result.ToolNames, tc.Function.Name)
			}

			// Append assistant message WITH tool_calls to the message list.
			assistantMsg := provider.Message{
				Role:      "assistant",
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			}
			msgs = append(msgs, assistantMsg)

			// Execute tools and append results.
			toolResultMsgs := executor.ExecuteToolCalls(ctx, resp.ToolCalls)
			msgs = append(msgs, toolResultMsgs...)

			continue
		}

		// No tool calls → final response.
		if resp.Content == "" {
			result.Messages = msgs
			return result, nil
		}

		result.Content = resp.Content
		result.Usage = &resp.Usage
		result.Messages = msgs
		return result, nil
	}

	// Max iterations reached — ask LLM to summarize without tools.
	log.Printf("[engine] max iterations (%d) reached, requesting summary", maxIter)
	summaryMsgs := append(msgs, provider.Message{
		Role:    "user",
		Content: "You've reached the tool execution limit. Please summarize what you accomplished and what remains to be done, without calling any more tools.",
	})
	summaryReq := provider.ChatRequest{
		Model:    opts.Model,
		Messages: summaryMsgs,
		// No tools — force text-only response.
	}
	summaryResp, err := e.Provider.Chat(ctx, summaryReq)
	if err != nil || summaryResp.Content == "" {
		result.Messages = msgs
		return result, fmt.Errorf("tool execution limit reached")
	}

	result.Content = summaryResp.Content
	result.Usage = &summaryResp.Usage
	result.Messages = msgs
	return result, nil
}

// ToolNamesSummary returns a comma-separated list of tool names from the result.
func (r *Result) ToolNamesSummary() string {
	if len(r.ToolNames) == 0 {
		return ""
	}
	return strings.Join(r.ToolNames, ", ")
}
