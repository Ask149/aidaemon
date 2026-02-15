package builtin

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ask149/aidaemon/internal/workspace"
)

// WriteWorkspaceTool writes content to agent-writable workspace files
// (MEMORY.md and TOOLS.md only).
type WriteWorkspaceTool struct {
	// WorkspaceDir is the path to the workspace directory.
	WorkspaceDir string
}

func (t *WriteWorkspaceTool) Name() string {
	return "write_workspace"
}

func (t *WriteWorkspaceTool) Description() string {
	return "Write content to an agent-writable workspace file. Only MEMORY.md and TOOLS.md are allowed. Use MEMORY.md to persist notes across conversations. Use TOOLS.md to record tool availability and usage patterns."
}

func (t *WriteWorkspaceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"MEMORY.md", "TOOLS.md"},
				"description": "Workspace file to write. Must be MEMORY.md or TOOLS.md.",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write to the file. Overwrites existing content.",
			},
		},
		"required": []string{"file", "content"},
	}
}

func (t *WriteWorkspaceTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	file, ok := args["file"].(string)
	if !ok {
		return "", fmt.Errorf("file must be a string")
	}

	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content must be a string")
	}

	// Reject path separators and traversal sequences in filename.
	if strings.ContainsAny(file, "/\\") || strings.Contains(file, "..") {
		return "", fmt.Errorf("invalid file name: %s", file)
	}

	// Check against workspace whitelist.
	if !workspace.IsAgentWritable(file) {
		return "", fmt.Errorf("access denied: %s is not agent-writable", file)
	}

	// Belt-and-suspenders: resolve absolute path and verify it stays within workspace.
	target := filepath.Join(t.WorkspaceDir, file)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	absDir, err := filepath.Abs(t.WorkspaceDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace dir: %w", err)
	}
	if !strings.HasPrefix(absTarget, absDir+string(filepath.Separator)) {
		return "", fmt.Errorf("access denied: resolved path escapes workspace directory")
	}

	// Write the file.
	if err := os.WriteFile(absTarget, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write workspace file: %w", err)
	}

	log.Printf("[write_workspace] wrote %d bytes to %s", len(content), file)
	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), file), nil
}
