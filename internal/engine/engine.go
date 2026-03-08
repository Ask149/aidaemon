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
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// OnProgress is called at key points during the engine loop to report
	// status. The Telegram bot uses this to update the placeholder message
	// so the user sees what's happening during long-running tasks.
	OnProgress func(ProgressUpdate)
}

// ProgressPhase describes what the engine is currently doing.
type ProgressPhase string

const (
	PhaseThinking      ProgressPhase = "thinking"
	PhaseExecutingTool ProgressPhase = "executing_tools"
	PhaseSummarizing   ProgressPhase = "summarizing"
)

// ProgressUpdate is sent via OnProgress to report engine loop status.
type ProgressUpdate struct {
	Phase        ProgressPhase
	Iteration    int
	TotalElapsed time.Duration
	ToolNames    []string // tool names being executed (when Phase == PhaseExecutingTool)
	ToolCount    int      // number of tools in this batch
	Message      string   // human-readable status line
}

// ToolExecution records a single tool call with timing.
type ToolExecution struct {
	Name     string        // tool name
	Duration time.Duration // wall-clock time for execution
	Error    bool          // true if the tool returned an error
}

// Result contains everything produced by a single engine run.
type Result struct {
	// Content is the final text response from the LLM.
	Content string

	// Usage is the token usage from the final LLM call.
	Usage *provider.Usage

	// TotalUsage is the cumulative token usage across all LLM calls in the run.
	TotalUsage provider.Usage

	// ToolIterations is how many tool-call iterations occurred (0 = none).
	ToolIterations int

	// Messages is the full in-memory message list after the run,
	// including all tool_call and tool result messages.
	Messages []provider.Message

	// ToolNames lists all tool names that were called during the run.
	ToolNames []string

	// ToolExecutions records per-tool timing and error info.
	ToolExecutions []ToolExecution

	// LLMCalls is how many times the LLM was invoked (including retries).
	LLMCalls int

	// Duration is the total wall-clock time for the entire Run.
	Duration time.Duration

	// SummarizeCount is how many emergency summarizations were triggered.
	SummarizeCount int

	// TrimmedMessages is how many messages were trimmed to fit the token budget.
	TrimmedMessages int

	// InputMessages is the number of messages sent to the LLM on the final call.
	InputMessages int
}

// estimateTokens provides a conservative token count for a string.
// Uses ~3 characters per token (conservative vs the ~4 rule of thumb)
// because tool results often contain JSON/code with short tokens.
func estimateTokens(s string) int {
	return len(s) / 3
}

// EstimateMessageTokens estimates the token count for a single message.
// Exported so the bot layer can use it for fallback token estimation.
func EstimateMessageTokens(m provider.Message) int {
	tokens := estimateTokens(m.Content)
	for _, p := range m.ContentParts {
		tokens += estimateTokens(p.Text)
		if p.ImageURL != nil {
			// Images are roughly 85 tokens for low-res, 765 for high-res.
			tokens += 765
		}
	}
	if m.ToolCallID != "" {
		tokens += 10
	}
	for _, tc := range m.ToolCalls {
		tokens += estimateTokens(tc.Function.Arguments) + estimateTokens(tc.Function.Name) + 10
	}
	return tokens + 4 // message overhead
}

// truncateToolResult shortens overly long tool results to stay within budget.
func truncateToolResult(content string, maxChars int) string {
	if len(content) <= maxChars {
		return content
	}
	half := maxChars / 2
	return content[:half] + "\n\n... [truncated " + fmt.Sprintf("%d", len(content)-maxChars) + " chars] ...\n\n" + content[len(content)-half:]
}

// trimMessagesToFit removes older non-system messages to fit within tokenBudget.
// It preserves: the system prompt (index 0), the last user message, and recent
// tool call/result pairs. It trims from the oldest conversation messages first.
// This function does NOT mutate the input slice or its messages.
func trimMessagesToFit(msgs []provider.Message, tokenBudget int) []provider.Message {
	// First pass: copy and truncate any individual tool results that are excessively long.
	const maxToolResultChars = 30000 // ~7500 tokens per tool result
	copied := make([]provider.Message, len(msgs))
	copy(copied, msgs)
	for i := range copied {
		if copied[i].Role == "tool" && len(copied[i].Content) > maxToolResultChars {
			log.Printf("[engine] truncating tool result message %d from %d to %d chars", i, len(copied[i].Content), maxToolResultChars)
			copied[i].Content = truncateToolResult(copied[i].Content, maxToolResultChars)
		}
	}

	// Calculate total tokens.
	total := 0
	for _, m := range copied {
		total += EstimateMessageTokens(m)
	}

	if total <= tokenBudget {
		return copied
	}

	log.Printf("[engine] token estimate %d exceeds budget %d, trimming messages", total, tokenBudget)

	// Strategy: keep system prompt (first msg if role=system), keep the last 4 messages
	// (recent context), and progressively remove older middle messages.
	// When removing, skip tool_call/result pairs to avoid orphans.
	var systemMsg *provider.Message
	startIdx := 0
	if len(copied) > 0 && copied[0].Role == "system" {
		systemMsg = &copied[0]
		startIdx = 1
	}

	// Keep removing the oldest non-system message until we're under budget.
	// Remove tool_call + tool_result messages together to avoid orphans.
	trimmed := copied[startIdx:]
	for len(trimmed) > 2 && total > tokenBudget {
		// If the oldest message is an assistant with tool_calls, remove it
		// along with the subsequent tool result messages.
		if trimmed[0].Role == "assistant" && len(trimmed[0].ToolCalls) > 0 {
			removed := EstimateMessageTokens(trimmed[0])
			trimmed = trimmed[1:]
			total -= removed
			// Also remove the corresponding tool result messages.
			for len(trimmed) > 2 && trimmed[0].Role == "tool" {
				removed = EstimateMessageTokens(trimmed[0])
				trimmed = trimmed[1:]
				total -= removed
			}
			continue
		}
		// If it's a tool result without a preceding tool_call (orphaned), remove it.
		if trimmed[0].Role == "tool" {
			removed := EstimateMessageTokens(trimmed[0])
			trimmed = trimmed[1:]
			total -= removed
			continue
		}
		// Regular message — remove it.
		removed := EstimateMessageTokens(trimmed[0])
		trimmed = trimmed[1:]
		total -= removed
		log.Printf("[engine] removed message, saved ~%d tokens, total now ~%d", removed, total)
	}

	// Reassemble.
	result := make([]provider.Message, 0, len(trimmed)+1)
	if systemMsg != nil {
		result = append(result, *systemMsg)
	}
	result = append(result, trimmed...)
	return result
}

// ModelTokenLimit returns the known context window for common models.
// Returns 0 if unknown (no trimming will be applied).
func ModelTokenLimit(model string) int {
	limits := map[string]int{
		"claude-sonnet-4.5":    128000,
		"claude-sonnet-4":      128000,
		"claude-opus-4.5":      128000,
		"claude-opus-4.6":      128000,
		"claude-opus-4.6-fast": 128000,
		"claude-haiku-4.5":     128000,
		"gpt-4o":              128000,
		"gpt-4o-mini":         128000,
		"gpt-4.1":             128000,
		"gpt-5":               128000,
		"gpt-5-mini":          128000,
		"gpt-5.1":             128000,
		"gpt-5.2":             128000,
		"gemini-2.5-pro":      1000000,
		"gemini-3-pro-preview": 1000000,
		"gemini-3-flash-preview": 1000000,
	}
	if limit, ok := limits[model]; ok {
		return limit
	}
	return 0
}

// maxAutoSummarizeRetries is how many times the engine will attempt
// to auto-summarize the conversation context after hitting a token
// limit error from the API. Each retry aggressively shrinks the
// message history by summarizing the oldest half.
const maxAutoSummarizeRetries = 3

// reTokenLimitError matches the Copilot API's "prompt token count exceeds limit"
// error so we can extract the actual counts and auto-recover.
var reTokenLimitError = regexp.MustCompile(
	`prompt token count of (\d+) exceeds the limit of (\d+)`,
)

// isTokenLimitError checks if an error is a prompt-token-exceeded error
// and returns (actualTokens, limit, true) if so.
func isTokenLimitError(err error) (int, int, bool) {
	if err == nil {
		return 0, 0, false
	}
	matches := reTokenLimitError.FindStringSubmatch(err.Error())
	if len(matches) != 3 {
		return 0, 0, false
	}
	actual, _ := strconv.Atoi(matches[1])
	limit, _ := strconv.Atoi(matches[2])
	return actual, limit, true
}

// emergencySummarize uses a cheap model to compress the message list when the
// API reports that the prompt exceeds the token limit. It summarizes the
// oldest half of non-system messages, replacing them with a compact summary.
// Returns the new, shorter message list.
func (e *Engine) emergencySummarize(ctx context.Context, msgs []provider.Message, actualTokens, limit int) []provider.Message {
	log.Printf("[engine] 🚨 emergency summarize: actual=%d tokens, limit=%d, messages=%d",
		actualTokens, limit, len(msgs))

	// Separate system prompt from conversation messages.
	var systemMsg *provider.Message
	startIdx := 0
	if len(msgs) > 0 && msgs[0].Role == "system" {
		systemMsg = &msgs[0]
		startIdx = 1
	}

	convMsgs := msgs[startIdx:]
	if len(convMsgs) < 4 {
		// Too few messages to summarize — copy and aggressively truncate tool results.
		result := make([]provider.Message, len(msgs))
		copy(result, msgs)
		for i := range result {
			if result[i].Role == "tool" && len(result[i].Content) > 5000 {
				result[i].Content = truncateToolResult(result[i].Content, 5000)
			}
		}
		return result
	}

	// Decide how many messages to summarize.
	// If we're 2x over, summarize 75%; otherwise summarize 50%.
	ratio := float64(actualTokens) / float64(limit)
	summarizeCount := len(convMsgs) / 2
	if ratio > 1.5 {
		summarizeCount = len(convMsgs) * 3 / 4
	}
	if summarizeCount < 2 {
		summarizeCount = 2
	}
	if summarizeCount >= len(convMsgs) {
		summarizeCount = len(convMsgs) - 2 // always keep latest 2 messages
	}

	oldMsgs := convMsgs[:summarizeCount]
	keepMsgs := convMsgs[summarizeCount:]

	// Build a transcript of the old messages for summarization.
	// Truncate individual messages to keep the summary prompt itself small.
	var transcript strings.Builder
	for _, m := range oldMsgs {
		transcript.WriteString(m.Role)
		transcript.WriteString(": ")
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		transcript.WriteString(content)
		transcript.WriteString("\n")
	}

	log.Printf("[engine] summarizing %d messages (keeping %d recent), transcript=%d chars",
		summarizeCount, len(keepMsgs), transcript.Len())

	// Call a cheap model for the summary.
	summaryReq := provider.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []provider.Message{
			{
				Role: "system",
				Content: "Summarize the following conversation in 2-3 concise paragraphs. " +
					"Preserve ALL key facts: file paths, variable names, decisions, errors encountered, " +
					"and action items. This summary will replace the original messages to reduce context size.",
			},
			{
				Role:    "user",
				Content: transcript.String(),
			},
		},
	}

	summaryResp, err := e.Provider.Chat(ctx, summaryReq)
	if err != nil || summaryResp.Content == "" {
		log.Printf("[engine] emergency summarize failed: %v — falling back to hard truncation", err)
		// Fallback: just drop the old messages entirely.
		result := make([]provider.Message, 0, len(keepMsgs)+1)
		if systemMsg != nil {
			result = append(result, *systemMsg)
		}
		result = append(result, keepMsgs...)
		return result
	}

	// Reassemble: system prompt + summary + recent messages.
	summaryContent := "[Previous conversation summary — auto-compacted to fit token limit]\n" + summaryResp.Content
	result := make([]provider.Message, 0, len(keepMsgs)+2)
	if systemMsg != nil {
		result = append(result, *systemMsg)
	}
	result = append(result, provider.Message{Role: "system", Content: summaryContent})
	result = append(result, keepMsgs...)

	log.Printf("[engine] emergency summarize complete: %d messages → %d messages",
		len(msgs), len(result))
	return result
}

// Run executes the chat loop: send messages to LLM, execute tools,
// repeat until a final text response or max iterations.
//
// The provided messages slice is NOT modified; a copy is used internally.
// If the LLM hits the iteration limit, a summary request is made.
// If the API rejects the request for exceeding token limits, the engine
// auto-summarizes the conversation and retries (up to 3 times).
func (e *Engine) Run(ctx context.Context, messages []provider.Message, opts RunOptions) (*Result, error) {
	runStart := time.Now()

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

	// Determine token budget: reserve space for tool defs + output.
	// First check if the provider has dynamic model info with context limits.
	tokenLimit := 0
	if e.Provider != nil {
		for _, m := range e.Provider.Models() {
			if m.ID == opts.Model && m.MaxContextTokens > 0 {
				tokenLimit = m.MaxContextTokens
				break
			}
		}
	}
	if tokenLimit == 0 {
		tokenLimit = ModelTokenLimit(opts.Model)
	}
	tokenBudget := 0
	if tokenLimit > 0 {
		// Estimate tool definition tokens.
		toolDefTokens := 0
		for _, td := range toolDefs {
			toolDefTokens += estimateTokens(td.Function.Name) + estimateTokens(td.Function.Description) + 200 // params overhead
		}
		// Budget = limit - tool defs - output reserve (8K)
		tokenBudget = tokenLimit - toolDefTokens - 8000
		if tokenBudget < 10000 {
			tokenBudget = 10000
		}
		log.Printf("[engine] token budget: %d (limit=%d, tool_defs=~%d)", tokenBudget, tokenLimit, toolDefTokens)
	}

	result := &Result{}
	summarizeRetries := 0 // tracks how many times we've auto-summarized
	preTrimCount := len(msgs)

	// Helper to emit progress updates.
	emitProgress := func(phase ProgressPhase, iteration int, msg string, toolNames []string) {
		if opts.OnProgress != nil {
			opts.OnProgress(ProgressUpdate{
				Phase:        phase,
				Iteration:    iteration,
				TotalElapsed: time.Since(runStart),
				ToolNames:    toolNames,
				ToolCount:    len(toolNames),
				Message:      msg,
			})
		}
	}

	for i := 0; i < maxIter; i++ {
		// Trim messages to fit within token budget before each LLM call.
		if tokenBudget > 0 {
			preTrimCount = len(msgs)
			msgs = trimMessagesToFit(msgs, tokenBudget)
			if trimmed := preTrimCount - len(msgs); trimmed > 0 {
				result.TrimmedMessages += trimmed
			}
		}

		req := provider.ChatRequest{
			Model:    opts.Model,
			Messages: msgs,
			Tools:    toolDefs,
		}

		log.Printf("[engine] calling LLM (iteration %d, %d messages)", i+1, len(msgs))

		// Notify progress: about to call LLM.
		if i == 0 {
			emitProgress(PhaseThinking, i+1, "🤔 Thinking...", nil)
		} else {
			emitProgress(PhaseThinking, i+1,
				fmt.Sprintf("🤔 Processing results... (step %d, %s)",
					i+1, time.Since(runStart).Round(time.Second)), nil)
		}

		resp, err := e.Provider.Chat(ctx, req)
		result.LLMCalls++
		if err != nil {
			// Check if this is a token-limit error we can auto-recover from.
			if actualTokens, apiLimit, ok := isTokenLimitError(err); ok && summarizeRetries < maxAutoSummarizeRetries {
				summarizeRetries++
				result.SummarizeCount++
				log.Printf("[engine] ⚠️  token limit exceeded (%d/%d), auto-summarizing (attempt %d/%d)",
					actualTokens, apiLimit, summarizeRetries, maxAutoSummarizeRetries)

				emitProgress(PhaseSummarizing, i+1,
					fmt.Sprintf("📝 Compacting context... (attempt %d/%d)",
						summarizeRetries, maxAutoSummarizeRetries), nil)

				msgs = e.emergencySummarize(ctx, msgs, actualTokens, apiLimit)

				// Also recalibrate our token budget based on the API's actual limit.
				if apiLimit > 0 {
					tokenBudget = apiLimit - 12000 // more conservative reserve
					if tokenBudget < 10000 {
						tokenBudget = 10000
					}
				}

				// Don't increment i — retry the same iteration.
				i--
				continue
			}
			result.Duration = time.Since(runStart)
			return nil, fmt.Errorf("LLM error: %w", err)
		}

		// Reset summarize retries on success.
		summarizeRetries = 0

		// Accumulate usage across all LLM calls.
		result.TotalUsage.InputTokens += resp.Usage.InputTokens
		result.TotalUsage.OutputTokens += resp.Usage.OutputTokens
		result.TotalUsage.CachedTokens += resp.Usage.CachedTokens

		log.Printf("[engine] LLM response: finish_reason=%s, tool_calls=%d, content_len=%d",
			resp.FinishReason, len(resp.ToolCalls), len(resp.Content))

		// Check if LLM wants to call tools.
		if len(resp.ToolCalls) > 0 {
			result.ToolIterations++

			// Record tool names.
			var callNames []string
			for _, tc := range resp.ToolCalls {
				result.ToolNames = append(result.ToolNames, tc.Function.Name)
				callNames = append(callNames, tc.Function.Name)
			}

			// Notify progress: about to execute tools.
			if len(callNames) == 1 {
				// Shorten MCP tool names for display: "mcp_playwright_browser_click" → "browser_click"
				short := callNames[0]
				if parts := strings.SplitN(short, "_", 3); len(parts) == 3 && parts[0] == "mcp" {
					short = parts[2]
				}
				emitProgress(PhaseExecutingTool, i+1,
					fmt.Sprintf("🔧 Running %s... (%s)", short,
						time.Since(runStart).Round(time.Second)), callNames)
			} else {
				emitProgress(PhaseExecutingTool, i+1,
					fmt.Sprintf("🔧 Running %d tools... (%s)",
						len(callNames), time.Since(runStart).Round(time.Second)), callNames)
			}

			// Append assistant message WITH tool_calls to the message list.
			assistantMsg := provider.Message{
				Role:      "assistant",
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			}
			msgs = append(msgs, assistantMsg)

			// Execute tools and append results.
			toolStart := time.Now()
			toolResultMsgs := executor.ExecuteToolCalls(ctx, resp.ToolCalls)
			toolDuration := time.Since(toolStart)

			// Record per-tool execution stats.
			for j, tc := range resp.ToolCalls {
				isErr := false
				if j < len(toolResultMsgs) && strings.HasPrefix(toolResultMsgs[j].Content, "Error:") {
					isErr = true
				}
				// Divide total duration equally among concurrent tools (best approximation).
				result.ToolExecutions = append(result.ToolExecutions, ToolExecution{
					Name:     tc.Function.Name,
					Duration: toolDuration / time.Duration(len(resp.ToolCalls)),
					Error:    isErr,
				})
			}

			msgs = append(msgs, toolResultMsgs...)

			continue
		}

		// No tool calls → final response.
		result.Duration = time.Since(runStart)
		result.InputMessages = len(msgs)
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
	if err != nil {
		// If even the summary fails due to token limits, try with summarized context.
		if actualTokens, apiLimit, ok := isTokenLimitError(err); ok {
			log.Printf("[engine] summary request also hit token limit, emergency summarize")
			summaryMsgs = e.emergencySummarize(ctx, summaryMsgs, actualTokens, apiLimit)
			summaryMsgs = append(summaryMsgs, provider.Message{
				Role:    "user",
				Content: "Please summarize what you accomplished and what remains to be done.",
			})
			summaryResp, err = e.Provider.Chat(ctx, provider.ChatRequest{
				Model:    opts.Model,
				Messages: summaryMsgs,
			})
		}
	}
	if err != nil || summaryResp == nil || summaryResp.Content == "" {
		result.Duration = time.Since(runStart)
		result.Messages = msgs
		return result, fmt.Errorf("tool execution limit reached")
	}

	result.Duration = time.Since(runStart)
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

// ToolTimingSummary returns a formatted string of tool execution times.
func (r *Result) ToolTimingSummary() string {
	if len(r.ToolExecutions) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, te := range r.ToolExecutions {
		if i > 0 {
			sb.WriteString(", ")
		}
		name := te.Name
		// Shorten MCP tool names: "mcp_playwright_browser_click" → "browser_click"
		if parts := strings.SplitN(name, "_", 3); len(parts) == 3 && parts[0] == "mcp" {
			name = parts[2]
		}
		if te.Error {
			sb.WriteString(fmt.Sprintf("%s ❌", name))
		} else {
			sb.WriteString(fmt.Sprintf("%s %s", name, te.Duration.Round(time.Millisecond)))
		}
	}
	return sb.String()
}

// UniqueToolNames returns deduplicated tool names with call counts.
func (r *Result) UniqueToolNames() string {
	if len(r.ToolNames) == 0 {
		return ""
	}
	counts := make(map[string]int)
	order := make([]string, 0)
	for _, name := range r.ToolNames {
		// Shorten MCP names.
		short := name
		if parts := strings.SplitN(name, "_", 3); len(parts) == 3 && parts[0] == "mcp" {
			short = parts[2]
		}
		if counts[short] == 0 {
			order = append(order, short)
		}
		counts[short]++
	}
	var sb strings.Builder
	for i, name := range order {
		if i > 0 {
			sb.WriteString(", ")
		}
		if counts[name] > 1 {
			sb.WriteString(fmt.Sprintf("%s ×%d", name, counts[name]))
		} else {
			sb.WriteString(name)
		}
	}
	return sb.String()
}
