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
	"sort"
	"strings"
	"time"
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

// tokenBudgetChars is the character threshold (~2K tokens at 3 chars/token)
// above which we warn about prompt size.
const tokenBudgetChars = 6000

// DailyLog holds a single daily memory log file.
type DailyLog struct {
	Date    string // "2006-01-02"
	Content string
}

// Skill holds a loaded skill file.
type Skill struct {
	Name    string // filename without .md extension
	Content string
}

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

	// DailyLogs contains recent daily memory log contents (last 3 days).
	DailyLogs []DailyLog

	// Skills contains loaded skill files, sorted alphabetically.
	Skills []Skill

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

	// Load recent daily logs (last 3 days).
	w.DailyLogs = loadDailyLogs(dir, 3)

	total := len(w.Soul) + len(w.User) + len(w.Memory) + len(w.Tools)
	for _, dl := range w.DailyLogs {
		total += len(dl.Content)
	}
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
		soul = DefaultSoul
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

	// After TOOLS section, add daily logs.
	if len(w.DailyLogs) > 0 {
		var logParts []string
		for _, dl := range w.DailyLogs {
			logParts = append(logParts, dl.Content)
		}
		parts = append(parts, "## Recent Activity Logs\n\n"+strings.Join(logParts, "\n\n---\n\n"))
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

// loadDailyLogs reads memory/YYYY-MM-DD.md files from the last N days.
func loadDailyLogs(dir string, days int) []DailyLog {
	memDir := filepath.Join(dir, "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return nil
	}

	cutoff := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var logs []DailyLog

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Expect YYYY-MM-DD.md format.
		if len(name) != 13 || !strings.HasSuffix(name, ".md") {
			continue
		}
		dateStr := strings.TrimSuffix(name, ".md")
		// Validate date format.
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			continue
		}
		if dateStr < cutoff {
			continue
		}

		content := readFile(memDir, name)
		if content != "" {
			logs = append(logs, DailyLog{Date: dateStr, Content: content})
		}
	}
	return logs
}

// loadSkills reads *.md files from skillsDir, sorted alphabetically.
// Returns nil if the directory doesn't exist or is empty.
func loadSkills(skillsDir string) []Skill {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}

	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		content := readFile(skillsDir, name)
		if content == "" {
			continue
		}
		skills = append(skills, Skill{
			Name:    strings.TrimSuffix(name, ".md"),
			Content: content,
		})
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills
}
