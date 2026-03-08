package tools

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/mcp"
)

// MCPTool wraps an MCP server tool as a local Tool for the registry.
// It delegates Execute() to the MCP client's tools/call JSON-RPC method.
type MCPTool struct {
	client     *mcp.Client
	info       mcp.ToolInfo
	serverName string
}

// NewMCPTool creates a bridge tool from an MCP tool definition.
func NewMCPTool(client *mcp.Client, info mcp.ToolInfo, serverName string) *MCPTool {
	return &MCPTool{
		client:     client,
		info:       info,
		serverName: serverName,
	}
}

// Name returns a namespaced tool name: "server__toolname".
// This prevents collisions between tools from different MCP servers.
func (t *MCPTool) Name() string {
	// Replace hyphens with underscores for OpenAI compatibility.
	name := strings.ReplaceAll(t.info.Name, "-", "_")
	return fmt.Sprintf("mcp_%s_%s", t.serverName, name)
}

// Description returns the tool description prefixed with the server name.
func (t *MCPTool) Description() string {
	desc := t.info.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool from %s server", t.serverName)
	}
	return fmt.Sprintf("[%s] %s", t.serverName, desc)
}

// Parameters returns the JSON Schema from the MCP tool definition.
func (t *MCPTool) Parameters() map[string]interface{} {
	if t.info.InputSchema == nil {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}
	return t.info.InputSchema
}

// Execute calls the MCP server's tools/call method and returns the result.
// Image content blocks are encoded as [MCP_IMAGE:mime:base64] markers
// that the Telegram bot layer can detect and forward as photos.
func (t *MCPTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	log.Printf("[mcp:%s] calling tool: %s", t.serverName, t.info.Name)

	// 30-second timeout for MCP tool calls — prevents indefinite blocking
	// if the MCP server hangs (e.g., ID mismatch, slow browser automation).
	toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := t.client.CallTool(toolCtx, t.info.Name, args)
	if err != nil {
		return "", fmt.Errorf("[%s] %w", t.serverName, err)
	}

	if result.IsError {
		text := mcp.ExtractText(result)
		return "", fmt.Errorf("[%s] tool error: %s", t.serverName, text)
	}

	return mcp.ExtractText(result), nil
}

// MCPToolName returns the original (non-namespaced) MCP tool name.
func (t *MCPTool) MCPToolName() string {
	return t.info.Name
}

// ServerName returns the MCP server name (e.g. "playwright").
func (t *MCPTool) ServerName() string {
	return t.serverName
}

// Client returns the underlying MCP client for direct calls.
func (t *MCPTool) Client() *mcp.Client {
	return t.client
}
