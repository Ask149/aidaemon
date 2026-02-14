// Package builtin provides built-in tools for AIDaemon.
package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadFileTool reads contents of a local file.
type ReadFileTool struct {
	// AllowedPaths is a list of glob patterns for allowed paths.
	// Example: ["~/Documents/**", "~/Projects/**"]
	AllowedPaths []string
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read the contents of a file from the local filesystem. Returns the file contents as text. For binary files, returns an error."
}

func (t *ReadFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the file to read. Use ~ for home directory.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}

	// Expand ~ to home directory.
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot find home directory: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}

	// Clean path (resolve .., ., etc.)
	path = filepath.Clean(path)

	// Check permissions.
	if !t.isAllowed(path) {
		return "", fmt.Errorf("access denied: %s is not in allowed paths", path)
	}

	// Read file.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	// Check if binary.
	if isBinary(data) {
		return "", fmt.Errorf("cannot read binary file: %s", path)
	}

	return string(data), nil
}

// isAllowed checks if a path matches any of the allowed patterns.
func (t *ReadFileTool) isAllowed(path string) bool {
	// If no restrictions, allow all.
	if len(t.AllowedPaths) == 0 {
		return true
	}

	// Check each pattern.
	for _, pattern := range t.AllowedPaths {
		// Expand ~ in pattern.
		if strings.HasPrefix(pattern, "~") {
			home, _ := os.UserHomeDir()
			pattern = filepath.Join(home, pattern[1:])
		}

		// Convert glob to prefix check (simple version).
		// "~/Documents/**" → "~/Documents/"
		pattern = strings.TrimSuffix(pattern, "/**")
		pattern = strings.TrimSuffix(pattern, "/*")

		if strings.HasPrefix(path, pattern) {
			return true
		}
	}

	return false
}

// isBinary checks if data looks like binary content.
// Simple heuristic: if first 512 bytes contain null bytes, it's binary.
func isBinary(data []byte) bool {
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}

	for _, b := range sample {
		if b == 0 {
			return true
		}
	}

	return false
}
