// Package tools provides the tool registry and execution engine.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/Ask149/aidaemon/internal/permissions"
)

// Registry manages all available tools and handles execution.
type Registry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	perms   *permissions.Checker
	audit   io.Writer // audit log writer (nil = disabled)
}

// NewRegistry creates an empty tool registry.
func NewRegistry(perms *permissions.Checker) *Registry {
	if perms == nil {
		perms = permissions.NewChecker(nil)
	}
	return &Registry{
		tools: make(map[string]Tool),
		perms: perms,
	}
}

// Register adds a tool to the registry.
// Panics if a tool with the same name is already registered.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := t.Name()
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("tool %q already registered", name))
	}

	r.tools[name] = t
	log.Printf("[tools] registered: %s", name)
}

// Get retrieves a tool by name.
// Returns nil if the tool doesn't exist.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// Definitions returns all tools in OpenAI function calling format.
// Used when sending tool definitions to the LLM.
func (r *Registry) Definitions() []ToolDefinition {
	tools := r.List()
	defs := make([]ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = ToDefinition(t)
	}
	return defs
}

// Execute runs a tool with the given arguments.
//
// The arguments are provided as a JSON string (from the LLM's response).
// This method:
// 1. Looks up the tool by name
// 2. Parses the JSON arguments
// 3. Checks permissions
// 4. Executes the tool
// 5. Returns the result or error
func (r *Registry) Execute(ctx context.Context, toolName string, argsJSON string) (string, error) {
	// Look up tool.
	tool := r.Get(toolName)
	if tool == nil {
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}

	// Parse arguments.
	var args map[string]interface{}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments JSON: %w", err)
		}
	}

	// Check permissions.
	if err := r.checkPermissions(toolName, args); err != nil {
		log.Printf("[tools] permission denied: %s: %v", toolName, err)
		r.auditLog("DENIED", toolName, argsJSON, 0, err)
		return "", err
	}

	log.Printf("[tools] executing: %s %v", toolName, args)
	r.auditLog("CALL", toolName, argsJSON, 0, nil)

	// Execute tool.
	start := time.Now()
	result, err := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	if err != nil {
		log.Printf("[tools] error: %s: %v", toolName, err)
		r.auditLog("ERROR", toolName, "", elapsed, err)
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	log.Printf("[tools] success: %s (%d bytes)", toolName, len(result))
	r.auditLog("OK", toolName, fmt.Sprintf("%d bytes", len(result)), elapsed, nil)
	return result, nil
}

// Permissions returns the checker for external use.
func (r *Registry) Permissions() *permissions.Checker {
	return r.perms
}

// SetAuditWriter enables audit logging to the given writer.
// Pass nil to disable audit logging.
func (r *Registry) SetAuditWriter(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audit = w
}

// auditLog writes a structured audit entry if an audit writer is configured.
// Format: 2024-01-15T10:30:00Z STATUS tool_name detail [duration] [error]
func (r *Registry) auditLog(status, toolName, detail string, elapsed time.Duration, err error) {
	r.mu.RLock()
	w := r.audit
	r.mu.RUnlock()

	if w == nil {
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	entry := fmt.Sprintf("%s %s %s", ts, status, toolName)
	if detail != "" {
		entry += " " + detail
	}
	if elapsed > 0 {
		entry += fmt.Sprintf(" (%s)", elapsed.Round(time.Millisecond))
	}
	if err != nil {
		entry += fmt.Sprintf(" err=%v", err)
	}
	entry += "\n"

	// Best-effort write; don't block tool execution on audit failure.
	if _, werr := io.WriteString(w, entry); werr != nil {
		log.Printf("[audit] write failed: %v", werr)
	}
}

// checkPermissions enforces permission rules based on tool arguments.
func (r *Registry) checkPermissions(toolName string, args map[string]interface{}) error {
	// Check path-based arguments.
	for _, key := range []string{"path", "file_path", "filename", "directory"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				if err := r.perms.CheckPath(toolName, s); err != nil {
					return err
				}
			}
		}
	}
	// Check command-based arguments.
	for _, key := range []string{"command", "cmd"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				if err := r.perms.CheckCommand(toolName, s); err != nil {
					return err
				}
			}
		}
	}
	// Check URL/domain-based arguments.
	for _, key := range []string{"url", "uri"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				if err := r.perms.CheckDomain(toolName, s); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ExecuteAll executes multiple tool calls in sequence.
// Returns a slice of ToolResults, one per call.
// Continues executing even if individual tools fail.
func (r *Registry) ExecuteAll(ctx context.Context, calls []ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))

	for i, call := range calls {
		result, err := r.Execute(ctx, call.Function.Name, call.Function.Arguments)

		results[i] = ToolResult{
			ToolCallID: call.ID,
		}

		if err != nil {
			results[i].Content = fmt.Sprintf("Error: %v", err)
			results[i].IsError = true
		} else {
			results[i].Content = result
		}
	}

	return results
}
