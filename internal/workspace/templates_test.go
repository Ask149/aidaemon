package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaults_CreatesSoul(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileSoul))
	if err != nil {
		t.Fatalf("SOUL.md not created: %v", err)
	}
	if string(data) != DefaultSoul {
		t.Errorf("SOUL.md content mismatch:\ngot:  %q\nwant: %q", string(data), DefaultSoul)
	}
}

func TestEnsureDefaults_NoOverwrite(t *testing.T) {
	dir := t.TempDir()

	existing := "I am a custom soul. Do not overwrite me."
	writeTestFile(t, dir, FileSoul, existing)

	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, FileSoul))
	if err != nil {
		t.Fatalf("SOUL.md read: %v", err)
	}
	if string(data) != existing {
		t.Errorf("SOUL.md was overwritten:\ngot:  %q\nwant: %q", string(data), existing)
	}
}

func TestEnsureDefaults_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "workspace")

	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, FileSoul)); err != nil {
		t.Fatalf("SOUL.md not created in nested dir: %v", err)
	}
}

func TestMigrateSystemPrompt_CopiesPrompt(t *testing.T) {
	configDir := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "workspace")

	oldContent := "You are an old-style system prompt."
	writeTestFile(t, configDir, "system_prompt.md", oldContent)

	MigrateSystemPrompt(configDir, workspaceDir)

	data, err := os.ReadFile(filepath.Join(workspaceDir, FileSoul))
	if err != nil {
		t.Fatalf("SOUL.md not created by migration: %v", err)
	}
	if string(data) != oldContent {
		t.Errorf("migrated content mismatch:\ngot:  %q\nwant: %q", string(data), oldContent)
	}
}

func TestMigrateSystemPrompt_SkipsIfSoulExists(t *testing.T) {
	configDir := t.TempDir()
	workspaceDir := t.TempDir()

	// Write both old prompt and existing SOUL.md.
	writeTestFile(t, configDir, "system_prompt.md", "old prompt content")
	existingSoul := "I am the existing soul."
	writeTestFile(t, workspaceDir, FileSoul, existingSoul)

	MigrateSystemPrompt(configDir, workspaceDir)

	data, err := os.ReadFile(filepath.Join(workspaceDir, FileSoul))
	if err != nil {
		t.Fatalf("SOUL.md read: %v", err)
	}
	if string(data) != existingSoul {
		t.Errorf("SOUL.md was overwritten by migration:\ngot:  %q\nwant: %q", string(data), existingSoul)
	}
}

func TestMigrateSystemPrompt_SkipsIfNoOldPrompt(t *testing.T) {
	configDir := t.TempDir()
	workspaceDir := t.TempDir()

	// No system_prompt.md exists in configDir.
	MigrateSystemPrompt(configDir, workspaceDir)

	// SOUL.md should NOT be created.
	if _, err := os.Stat(filepath.Join(workspaceDir, FileSoul)); err == nil {
		t.Error("SOUL.md should not be created when no system_prompt.md exists")
	}
}
