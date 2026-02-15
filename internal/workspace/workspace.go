// Package workspace loads a directory of markdown files and assembles them
// into a system prompt for the AI engine.
//
// The workspace directory contains up to four files:
//   - SOUL.md  — persona and identity (user-editable only)
//   - USER.md  — user preferences and context (user-editable only)
//   - MEMORY.md — agent's evolving memory (agent-writable)
//   - TOOLS.md  — agent's notes about available tools (agent-writable)
//
// Missing files are silently skipped. The assembled prompt is refreshed
// on every message, so edits take effect immediately.
package workspace

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// File name constants for workspace markdown files.
const (
	FileSoul   = "SOUL.md"
	FileUser   = "USER.md"
	FileMemory = "MEMORY.md"
	FileTools  = "TOOLS.md"
)

// IsAgentWritable reports whether the named file can be modified by the agent.
func IsAgentWritable(name string) bool {
	return name == FileMemory || name == FileTools
}

// defaultSoul is used when no SOUL.md exists in the workspace.
const defaultSoul = "You are a helpful personal assistant. Be concise and direct."

// tokenBudgetChars is the character threshold (~2K tokens at 3 chars/token)
// above which we warn about prompt size.
const tokenBudgetChars = 6000

// Workspace holds the loaded contents of a workspace directory.
type Workspace struct {
	// Dir is the workspace directory path.
	Dir string

	// Soul is the content of SOUL.md (persona/identity).
	Soul string

	// User is the content of USER.md (user preferences).
	User string

	// Memory is the content of MEMORY.md (agent's evolving memory).
	Memory string

	// Tools is the content of TOOLS.md (agent's tool notes).
	Tools string

	// OverTokenBudget is true when the total workspace content exceeds
	// the character budget (~2K tokens).
	OverTokenBudget bool
}

// Load reads all workspace files from dir and returns a Workspace.
// Missing files are silently skipped (empty string). A non-existent
// directory is treated as an empty workspace.
func Load(dir string) *Workspace {
	w := &Workspace{Dir: dir}

	w.Soul = readFile(dir, FileSoul)
	w.User = readFile(dir, FileUser)
	w.Memory = readFile(dir, FileMemory)
	w.Tools = readFile(dir, FileTools)

	total := len(w.Soul) + len(w.User) + len(w.Memory) + len(w.Tools)
	if total > tokenBudgetChars {
		w.OverTokenBudget = true
		log.Printf("[workspace] token budget warning: workspace content is %d chars (limit %d), consider trimming", total, tokenBudgetChars)
	}

	return w
}

// SystemPrompt assembles the system prompt from all loaded workspace files.
// Order: SOUL.md (or default) → USER.md → MEMORY.md → TOOLS.md.
// Sections are separated by "---" and prefixed with headers.
func (w *Workspace) SystemPrompt() string {
	var parts []string

	// Soul (or default fallback).
	soul := w.Soul
	if soul == "" {
		soul = defaultSoul
	}
	parts = append(parts, soul)

	if w.User != "" {
		parts = append(parts, "## User Context\n\n"+w.User)
	}
	if w.Memory != "" {
		parts = append(parts, "## Your Memory\n\n"+w.Memory)
	}
	if w.Tools != "" {
		parts = append(parts, "## Tool Notes\n\n"+w.Tools)
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// readFile reads a single file from the workspace directory.
// Returns empty string if the file doesn't exist or can't be read.
func readFile(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
