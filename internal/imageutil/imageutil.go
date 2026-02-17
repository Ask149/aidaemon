// Package imageutil provides shared utilities for extracting and handling
// MCP image markers in tool results.
//
// MCP tools that return image content produce markers in the format
// [MCP_IMAGE:mime/type:base64data]. This package parses those markers,
// extracts the image data, and provides helpers for converting images
// to data URLs suitable for browser rendering.
package imageutil

import (
	"strings"
)

// Image represents an extracted image from an MCP tool result.
type Image struct {
	MimeType   string // e.g. "image/png"
	Base64Data string // raw base64-encoded image data
}

// DataURL converts an Image to a data URL string for embedding in HTML.
func DataURL(img Image) string {
	return "data:" + img.MimeType + ";base64," + img.Base64Data
}

// ParseImages extracts all [MCP_IMAGE:mime:base64data] markers from content.
// It returns the cleaned content (markers replaced with placeholder text)
// and a slice of extracted images. If no markers are found, images is nil.
func ParseImages(content string) (string, []Image) {
	var images []Image

	for {
		start := strings.Index(content, "[MCP_IMAGE:")
		if start == -1 {
			break
		}
		end := strings.Index(content[start:], "]")
		if end == -1 {
			break
		}
		end += start

		// Parse [MCP_IMAGE:mime:base64data]
		marker := content[start+len("[MCP_IMAGE:") : end]
		parts := strings.SplitN(marker, ":", 2)
		if len(parts) != 2 {
			// Malformed marker — skip past it to avoid infinite loop.
			content = content[:start] + "[malformed image marker]" + content[end+1:]
			continue
		}

		images = append(images, Image{
			MimeType:   parts[0],
			Base64Data: parts[1],
		})

		content = content[:start] + "[Screenshot delivered]" + content[end+1:]
	}

	return content, images
}

// autoScreenshotTools lists Playwright tool names (without the "mcp_playwright_"
// prefix) that change visible page state and should trigger an automatic
// screenshot after execution.
var autoScreenshotTools = map[string]bool{
	"browser_navigate":      true,
	"browser_navigate_back": true,
	"browser_click":         true,
	"browser_type":          true,
	"browser_fill_form":     true,
	"browser_select_option": true,
	"browser_drag":          true,
	"browser_press_key":     true,
	"browser_hover":         true,
	"browser_handle_dialog": true,
	"browser_file_upload":   true,
}

// ShouldAutoScreenshot reports whether the given namespaced MCP tool name
// (e.g. "mcp_playwright_browser_click") should trigger an automatic
// screenshot after execution.
func ShouldAutoScreenshot(toolName string) bool {
	if !strings.HasPrefix(toolName, "mcp_playwright_") {
		return false
	}
	bareName := strings.TrimPrefix(toolName, "mcp_playwright_")
	return autoScreenshotTools[bareName]
}
