// Package tools provides the tool registry and execution engine.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
)

// Registry manages all available tools and handles execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
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
// 3. Executes the tool
// 4. Returns the result or error
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

	log.Printf("[tools] executing: %s %v", toolName, args)

	// Execute tool.
	result, err := tool.Execute(ctx, args)
	if err != nil {
		log.Printf("[tools] error: %s: %v", toolName, err)
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	log.Printf("[tools] success: %s (%d bytes)", toolName, len(result))
	return result, nil
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
