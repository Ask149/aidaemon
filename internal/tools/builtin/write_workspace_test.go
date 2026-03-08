package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteWorkspaceTool_Name(t *testing.T) {
	tool := &WriteWorkspaceTool{WorkspaceDir: t.TempDir()}
	if got := tool.Name(); got != "write_workspace" {
		t.Errorf("Name() = %q, want %q", got, "write_workspace")
	}
}

func TestWriteWorkspaceTool_WritesAllowed(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
	}{
		{"MEMORY.md", "MEMORY.md", "Remember: user prefers dark mode."},
		{"TOOLS.md", "TOOLS.md", "MCP server runs on port 8080."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tool := &WriteWorkspaceTool{WorkspaceDir: dir}

			result, err := tool.Execute(context.Background(), map[string]interface{}{
				"file":    tt.file,
				"content": tt.content,
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result == "" {
				t.Fatal("Execute() returned empty result")
			}

			data, err := os.ReadFile(filepath.Join(dir, tt.file))
			if err != nil {
				t.Fatalf("reading %s from disk: %v", tt.file, err)
			}
			if string(data) != tt.content {
				t.Errorf("file content = %q, want %q", string(data), tt.content)
			}
		})
	}
}

func TestWriteWorkspaceTool_RejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{"non-writable SOUL.md", "SOUL.md"},
		{"non-writable USER.md", "USER.md"},
		{"path traversal", "../etc/passwd"},
		{"traversal with allowed name", "../../MEMORY.md"},
		{"wrong case", "memory.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tool := &WriteWorkspaceTool{WorkspaceDir: dir}

			_, err := tool.Execute(context.Background(), map[string]interface{}{
				"file":    tt.file,
				"content": "should not be written",
			})
			if err == nil {
				t.Fatalf("Execute() should reject file %q", tt.file)
			}
		})
	}
}
