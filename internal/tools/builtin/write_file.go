// Package builtin provides built-in tools for AIDaemon.
package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileTool writes content to a local file.
type WriteFileTool struct {
	// AllowedPaths is a list of glob patterns for allowed paths.
	AllowedPaths []string
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Write content to a file on the local filesystem. Creates the file if it doesn't exist, overwrites if it does. Creates parent directories as needed."
}

func (t *WriteFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to the file to write. Use ~ for home directory.",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write to the file.",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path must be a string")
	}

	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be a string")
	}

	// Expand ~ to home directory.
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot find home directory: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}

	// Clean path.
	path = filepath.Clean(path)

	// Check permissions.
	if !t.isAllowed(path) {
		return "", fmt.Errorf("access denied: %s is not in allowed paths", path)
	}

	// Create parent directories.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	// Write file.
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
}

// isAllowed checks if a path matches any of the allowed patterns.
func (t *WriteFileTool) isAllowed(path string) bool {
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

		// Convert glob to prefix check.
		pattern = strings.TrimSuffix(pattern, "/**")
		pattern = strings.TrimSuffix(pattern, "/*")

		if strings.HasPrefix(path, pattern) {
			return true
		}
	}

	return false
}
