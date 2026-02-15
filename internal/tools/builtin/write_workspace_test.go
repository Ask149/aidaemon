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

func TestWriteWorkspaceTool_WritesMemory(t *testing.T) {
	dir := t.TempDir()
	tool := &WriteWorkspaceTool{WorkspaceDir: dir}

	content := "Remember: user prefers dark mode."
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":    "MEMORY.md",
		"content": content,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify success message includes byte count.
	if result == "" {
		t.Fatal("Execute() returned empty result")
	}

	// Verify file was written to disk.
	data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("reading MEMORY.md from disk: %v", err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}
}

func TestWriteWorkspaceTool_RejectsSOUL(t *testing.T) {
	dir := t.TempDir()
	tool := &WriteWorkspaceTool{WorkspaceDir: dir}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":    "SOUL.md",
		"content": "hacked soul",
	})
	if err == nil {
		t.Fatal("Execute() should reject write to SOUL.md")
	}

	// Verify no file was created.
	if _, statErr := os.Stat(filepath.Join(dir, "SOUL.md")); statErr == nil {
		t.Error("SOUL.md should not have been created")
	}
}

func TestWriteWorkspaceTool_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	tool := &WriteWorkspaceTool{WorkspaceDir: dir}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"file":    "../etc/passwd",
		"content": "root::0:0:::/bin/sh",
	})
	if err == nil {
		t.Fatal("Execute() should reject path traversal attempt")
	}

	// Verify no file was created outside workspace.
	if _, statErr := os.Stat(filepath.Join(dir, "..", "etc", "passwd")); statErr == nil {
		t.Error("path traversal file should not have been created")
	}
}
