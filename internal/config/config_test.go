package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceDir_Default(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.WorkspaceDir != "" {
		t.Errorf("expected empty default WorkspaceDir, got %q", cfg.WorkspaceDir)
	}
}

func TestResolveWorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{WorkspaceDir: dir}
	wsDir := cfg.ResolvedWorkspaceDir()
	if wsDir != dir {
		t.Errorf("expected %q, got %q", dir, wsDir)
	}
}

func TestResolveWorkspaceDir_Default(t *testing.T) {
	cfg := &Config{}
	wsDir := cfg.ResolvedWorkspaceDir()
	// Should end with .config/aidaemon/workspace
	want := filepath.Join(".config", "aidaemon", "workspace")
	if !strings.HasSuffix(wsDir, want) {
		t.Errorf("expected default workspace path ending with %q, got %q", want, wsDir)
	}
}
