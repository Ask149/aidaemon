package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadWorkspace_Empty(t *testing.T) {
	dir := t.TempDir()

	w := Load(dir)

	prompt := w.SystemPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty default system prompt from empty dir")
	}
	if !strings.Contains(prompt, "AIDaemon") {
		t.Errorf("expected DefaultSoul in prompt, got: %s", prompt)
	}
}

func TestLoadWorkspace_WithSoul(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, FileSoul, "I am a pirate assistant. Arrr!")

	w := Load(dir)

	prompt := w.SystemPrompt()
	if !strings.Contains(prompt, "pirate assistant") {
		t.Errorf("expected SOUL.md content in prompt, got: %s", prompt)
	}
	if strings.Contains(prompt, "AIDaemon") {
		t.Errorf("DefaultSoul should not appear when SOUL.md exists")
	}
}

func TestLoadWorkspace_AllFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, FileSoul, "I am the soul.")
	writeTestFile(t, dir, FileUser, "User likes Go.")
	writeTestFile(t, dir, FileMemory, "Remember: user is Ashish.")
	writeTestFile(t, dir, FileTools, "mcp server available on port 8080.")

	w := Load(dir)
	prompt := w.SystemPrompt()

	// Verify all sections present.
	checks := []struct {
		label    string
		expected string
	}{
		{"soul content", "I am the soul."},
		{"user header", "## User Context"},
		{"user content", "User likes Go."},
		{"memory header", "## Your Memory"},
		{"memory content", "Remember: user is Ashish."},
		{"tools header", "## Tool Notes"},
		{"tools content", "mcp server available on port 8080."},
		{"separator", "---"},
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c.expected) {
			t.Errorf("%s: expected %q in prompt, got:\n%s", c.label, c.expected, prompt)
		}
	}

	// Verify order: soul before user before memory before tools.
	soulIdx := strings.Index(prompt, "I am the soul.")
	userIdx := strings.Index(prompt, "## User Context")
	memIdx := strings.Index(prompt, "## Your Memory")
	toolIdx := strings.Index(prompt, "## Tool Notes")

	if soulIdx >= userIdx {
		t.Errorf("soul (%d) should come before user (%d)", soulIdx, userIdx)
	}
	if userIdx >= memIdx {
		t.Errorf("user (%d) should come before memory (%d)", userIdx, memIdx)
	}
	if memIdx >= toolIdx {
		t.Errorf("memory (%d) should come before tools (%d)", memIdx, toolIdx)
	}
}

func TestLoadWorkspace_TokenBudget(t *testing.T) {
	tests := []struct {
		name     string
		size     int
		wantOver bool
	}{
		{"at_limit", 6000, false},
		{"over_limit", 6001, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, FileMemory, strings.Repeat("x", tt.size))
			w := Load(dir)
			if w.OverTokenBudget != tt.wantOver {
				t.Errorf("size=%d: got OverTokenBudget=%v, want %v", tt.size, w.OverTokenBudget, tt.wantOver)
			}
		})
	}
}

func TestLoadWorkspace_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent", "path")

	w := Load(dir)

	// Should return defaults without panicking.
	if w == nil {
		t.Fatal("expected non-nil workspace for missing dir")
	}
	prompt := w.SystemPrompt()
	if !strings.Contains(prompt, "AIDaemon") {
		t.Errorf("expected DefaultSoul for missing dir, got: %s", prompt)
	}
	if w.OverTokenBudget {
		t.Error("empty workspace should not be over budget")
	}
}

func TestAgentWritableFiles(t *testing.T) {
	if !IsAgentWritable(FileMemory) {
		t.Error("MEMORY.md should be agent-writable")
	}
	if !IsAgentWritable(FileTools) {
		t.Error("TOOLS.md should be agent-writable")
	}
	if IsAgentWritable(FileSoul) {
		t.Error("SOUL.md should not be agent-writable")
	}
	if IsAgentWritable(FileUser) {
		t.Error("USER.md should not be agent-writable")
	}
}

func TestLoad_IncludesRecentDailyLogs(t *testing.T) {
	dir := t.TempDir()

	// Create memory directory with daily logs.
	memDir := filepath.Join(dir, "memory")
	os.MkdirAll(memDir, 0700)

	today := time.Now()
	for i := 0; i < 5; i++ {
		date := today.AddDate(0, 0, -i)
		name := date.Format("2006-01-02") + ".md"
		os.WriteFile(filepath.Join(memDir, name), []byte("# Log "+name), 0644)
	}

	ws := Load(dir)

	// Verify exactly 3 days are loaded (today, yesterday, 2 days ago).
	if len(ws.DailyLogs) != 3 {
		t.Errorf("expected 3 daily logs, got %d", len(ws.DailyLogs))
	}

	// SystemPrompt should include last 3 days only.
	prompt := ws.SystemPrompt()
	todayStr := today.Format("2006-01-02")
	if !strings.Contains(prompt, todayStr) {
		t.Error("expected today's log in prompt")
	}

	// 4 days ago should NOT be in prompt.
	oldDate := today.AddDate(0, 0, -4).Format("2006-01-02")
	if strings.Contains(prompt, oldDate) {
		t.Error("old log should not be in prompt")
	}
}

func TestLoadSkills_Empty(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	// No skills dir created — should return nil.
	skills := loadSkills(skillsDir)
	if skills != nil {
		t.Errorf("expected nil skills for missing dir, got %v", skills)
	}
}

func TestLoadSkills_LoadsAndSorts(t *testing.T) {
	skillsDir := t.TempDir()
	writeTestFile(t, skillsDir, "zebra.md", "Zebra skill content")
	writeTestFile(t, skillsDir, "alpha.md", "Alpha skill content")
	writeTestFile(t, skillsDir, "middle.md", "Middle skill content")
	// Non-md file should be ignored.
	writeTestFile(t, skillsDir, "ignore.txt", "Not a skill")

	skills := loadSkills(skillsDir)

	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}
	// Alphabetical order.
	if skills[0].Name != "alpha" {
		t.Errorf("expected first skill 'alpha', got %q", skills[0].Name)
	}
	if skills[1].Name != "middle" {
		t.Errorf("expected second skill 'middle', got %q", skills[1].Name)
	}
	if skills[2].Name != "zebra" {
		t.Errorf("expected third skill 'zebra', got %q", skills[2].Name)
	}
	if skills[0].Content != "Alpha skill content" {
		t.Errorf("expected alpha content, got %q", skills[0].Content)
	}
}

func TestLoadSkills_SkipsEmptyFiles(t *testing.T) {
	skillsDir := t.TempDir()
	writeTestFile(t, skillsDir, "real.md", "Real content")
	writeTestFile(t, skillsDir, "empty.md", "")
	writeTestFile(t, skillsDir, "whitespace.md", "   \n  \n  ")

	skills := loadSkills(skillsDir)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill (empty/whitespace skipped), got %d", len(skills))
	}
	if skills[0].Name != "real" {
		t.Errorf("expected 'real', got %q", skills[0].Name)
	}
}

// writeTestFile is a test helper that writes content to a file in dir.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeTestFile(%s): %v", name, err)
	}
}
