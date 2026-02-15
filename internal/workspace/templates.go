package workspace

import (
	"log"
	"os"
	"path/filepath"
)

// DefaultSoul is the default SOUL.md template for fresh installs.
const DefaultSoul = `# AIDaemon

You are AIDaemon, a personal AI assistant running as a daemon on the user's machine.

## Personality
- Concise and direct
- Proactive about using tools when they'd help
- Honest about limitations

## Guidelines
- Use write_workspace to persist important learnings to MEMORY.md
- Keep MEMORY.md under ~2000 tokens — summarize and prune regularly
- Use TOOLS.md to note tool quirks and usage tips you discover
`

// EnsureDefaults creates default workspace files if they don't exist.
// Does NOT overwrite existing files.
func EnsureDefaults(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Only seed SOUL.md if nothing exists yet.
	defaults := map[string]string{
		FileSoul: DefaultSoul,
	}

	for name, content := range defaults {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return err
			}
			log.Printf("[workspace] seeded default %s", name)
		}
	}
	return nil
}

// MigrateSystemPrompt copies system_prompt.md to workspace/SOUL.md if needed.
// Call once at startup. Idempotent — does nothing if SOUL.md already exists.
func MigrateSystemPrompt(configDir, workspaceDir string) {
	soulPath := filepath.Join(workspaceDir, FileSoul)
	if _, err := os.Stat(soulPath); err == nil {
		return // SOUL.md already exists, skip.
	} else if !os.IsNotExist(err) {
		log.Printf("[workspace] cannot stat %s: %v", soulPath, err)
		return // Unexpected error (e.g., permission denied), don't attempt migration.
	}

	oldPath := filepath.Join(configDir, "system_prompt.md")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return // No old prompt, skip.
	}

	os.MkdirAll(workspaceDir, 0700) //nolint:errcheck // best-effort
	if err := os.WriteFile(soulPath, data, 0644); err != nil {
		log.Printf("[workspace] migration failed: %v", err)
		return
	}
	log.Printf("[workspace] migrated system_prompt.md → SOUL.md")
}
