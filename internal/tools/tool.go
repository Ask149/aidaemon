// Package tools provides the tool/function calling framework for AIDaemon.
//
// Tools are capabilities that the LLM can invoke to interact with the
// outside world: reading files, running commands, fetching web pages, etc.
//
// The framework follows OpenAI's function calling specification, which
// Copilot's API supports natively.
package tools

import (
	"context"
)

// Tool represents a callable function that the LLM can invoke.
//
// Each tool must provide:
// - A unique name (e.g. "read_file")
// - A description explaining what it does
// - A JSON Schema describing its parameters
// - An Execute method that performs the actual work
type Tool interface {
	// Name returns the unique identifier for this tool.
	// Must be lowercase with underscores (e.g. "read_file", "web_search").
	Name() string

	// Description returns a human-readable explanation of what this tool does.
	// The LLM uses this to decide when to call the tool.
	Description() string

	// Parameters returns a JSON Schema describing the tool's input.
	// Example:
	//   {
	//     "type": "object",
	//     "properties": {
	//       "path": {"type": "string", "description": "File path"}
	//     },
	//     "required": ["path"]
	//   }
	Parameters() map[string]interface{}

	// Execute runs the tool with the given arguments.
	// Args are validated against Parameters() before calling.
	// Returns the tool output as a string, or an error.
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// ToolDefinition is the OpenAI function calling format.
// Used when sending tool definitions to the LLM.
type ToolDefinition struct {
	Type     string                 `json:"type"`     // Always "function"
	Function FunctionDefinition     `json:"function"`
}

// FunctionDefinition describes a callable function.
type FunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall represents a tool invocation from the LLM.
// Matches OpenAI's tool_calls response format.
type ToolCall struct {
	ID       string       `json:"id"`        // Unique call ID from LLM
	Type     string       `json:"type"`      // Always "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolResult is the result of executing a tool.
// Sent back to the LLM as a message with role "tool".
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// ToDefinition converts a Tool to OpenAI's ToolDefinition format.
func ToDefinition(t Tool) ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}
