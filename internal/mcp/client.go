package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Client provides a high-level interface to an MCP server.
// Created by Server.Start() after initializing the subprocess.
type Client struct {
	transport  *Transport
	serverInfo Info
	serverName string // human-readable name from config (e.g. "playwright")
}

// NewClient wraps a transport with high-level MCP operations.
func NewClient(transport *Transport, serverName string) *Client {
	return &Client{
		transport:  transport,
		serverName: serverName,
	}
}

// Initialize performs the MCP protocol handshake.
func (c *Client) Initialize() error {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    Capabilities{},
		ClientInfo: Info{
			Name:    "aidaemon",
			Version: "0.2.0",
		},
	}

	resp, err := c.transport.Send("initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	c.serverInfo = result.ServerInfo
	log.Printf("[mcp:%s] initialized: server=%s, protocol=%s",
		c.serverName, result.ServerInfo.Name, result.ProtocolVersion)

	// Send initialized notification.
	if err := c.transport.Notify("notifications/initialized", nil); err != nil {
		log.Printf("[mcp:%s] warning: initialized notification failed: %v", c.serverName, err)
	}

	return nil
}

// ListTools retrieves all tools exposed by the server.
func (c *Client) ListTools() ([]ToolInfo, error) {
	resp, err := c.transport.Send("tools/list", struct{}{})
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var result ListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	log.Printf("[mcp:%s] discovered %d tools", c.serverName, len(result.Tools))
	for _, t := range result.Tools {
		log.Printf("[mcp:%s]   • %s", c.serverName, t.Name)
	}

	return result.Tools, nil
}

// CallTool invokes a tool by name with the given arguments.
// Returns the concatenated text content and any image blocks.
func (c *Client) CallTool(name string, args map[string]interface{}) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}

	resp, err := c.transport.Send("tools/call", params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", name, err)
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	return &result, nil
}

// ServerName returns the config name for this server.
func (c *Client) ServerName() string {
	return c.serverName
}

// ServerInfo returns the info reported by the server during initialization.
func (c *Client) ServerInfo() Info {
	return c.serverInfo
}

// ExtractText concatenates all text content blocks from a CallToolResult.
// Image blocks are returned as [IMAGE:base64data:mimeType] markers.
func ExtractText(result *CallToolResult) string {
	var sb strings.Builder
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			sb.WriteString(block.Text)
		case "image":
			// Encode as a marker the bot layer can detect and send as a photo.
			mime := block.MimeType
			if mime == "" {
				mime = "image/png"
			}
			sb.WriteString(fmt.Sprintf("[MCP_IMAGE:%s:%s]", mime, block.Data))
		default:
			sb.WriteString(fmt.Sprintf("[%s content]", block.Type))
		}
	}
	return sb.String()
}
