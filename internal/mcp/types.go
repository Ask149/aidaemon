// Package mcp implements a Model Context Protocol (MCP) client.
//
// MCP uses JSON-RPC 2.0 over stdio to communicate with external tool servers.
// Protocol version: 2024-11-05
//
// See: https://modelcontextprotocol.io/
package mcp

import (
	"encoding/json"
	"fmt"
)

// --- JSON-RPC 2.0 ---

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// --- MCP Protocol Types ---

// InitializeParams is sent by the client to start a session.
type InitializeParams struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	ClientInfo      Info         `json:"clientInfo"`
}

// InitializeResult is returned by the server after initialization.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Info               `json:"serverInfo"`
}

// Info identifies a client or server.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Capabilities advertises what the client supports.
type Capabilities struct{}

// ServerCapabilities advertises what the server supports.
type ServerCapabilities struct {
	Tools     *ToolsCapability   `json:"tools,omitempty"`
	Resources *json.RawMessage   `json:"resources,omitempty"`
	Prompts   *json.RawMessage   `json:"prompts,omitempty"`
}

// ToolsCapability indicates tool support.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// --- Tool types ---

// ToolInfo describes a tool exposed by an MCP server.
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ListToolsResult is the response to tools/list.
type ListToolsResult struct {
	Tools []ToolInfo `json:"tools"`
}

// CallToolParams is sent to invoke a tool.
type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// CallToolResult is the response from a tool invocation.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is a piece of content returned by a tool.
type ContentBlock struct {
	Type     string `json:"type"`               // "text", "image", "resource"
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`      // base64 for images
	MimeType string `json:"mimeType,omitempty"`  // e.g. "image/png"
}

// --- Config ---

// ServerConfig describes how to launch an MCP server subprocess.
type ServerConfig struct {
	// Command to run (e.g. "npx", "uvx", "node", "bunx").
	Command string `json:"command"`

	// Args passed to the command.
	Args []string `json:"args"`

	// Env is additional environment variables (merged with os.Environ).
	Env map[string]string `json:"env,omitempty"`

	// Enabled allows disabling a server without removing its config (default: true).
	Enabled *bool `json:"enabled,omitempty"`
}

// IsEnabled returns true unless explicitly disabled.
func (c ServerConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}
