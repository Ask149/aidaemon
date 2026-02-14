package permissions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckerNoRules(t *testing.T) {
	c := NewChecker(nil)

	// No rules = allow everything.
	if err := c.CheckPath("read_file", "/etc/passwd"); err != nil {
		t.Errorf("expected allow, got: %v", err)
	}
	if err := c.CheckCommand("run_command", "rm -rf /"); err != nil {
		t.Errorf("expected allow, got: %v", err)
	}
	if err := c.CheckDomain("web_fetch", "https://evil.com"); err != nil {
		t.Errorf("expected allow, got: %v", err)
	}
}

func TestCheckerUnknownTool(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"read_file": {Mode: ModeWhitelist, AllowedPaths: []string{"/safe"}},
	})

	// Tool not in rules = allow.
	if err := c.CheckPath("write_file", "/anywhere"); err != nil {
		t.Errorf("expected allow for unknown tool, got: %v", err)
	}
}

// --- CheckPath ---

func TestCheckPath_AllowAll(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"read_file": {Mode: ModeAllowAll},
	})
	if err := c.CheckPath("read_file", "/any/path"); err != nil {
		t.Errorf("allow_all should permit any path: %v", err)
	}
}

func TestCheckPath_EmptyMode(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"read_file": {Mode: ""},
	})
	if err := c.CheckPath("read_file", "/any/path"); err != nil {
		t.Errorf("empty mode should default to allow: %v", err)
	}
}

func TestCheckPath_Whitelist(t *testing.T) {
	home, _ := os.UserHomeDir()
	c := NewChecker(map[string]Rule{
		"read_file": {
			Mode:         ModeWhitelist,
			AllowedPaths: []string{"~/Documents/**"},
		},
	})

	// Allowed path.
	allowed := filepath.Join(home, "Documents", "test.txt")
	if err := c.CheckPath("read_file", allowed); err != nil {
		t.Errorf("expected allow for %s: %v", allowed, err)
	}

	// Denied path.
	denied := "/etc/passwd"
	if err := c.CheckPath("read_file", denied); err == nil {
		t.Errorf("expected deny for %s", denied)
	}
}

func TestCheckPath_Deny(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"read_file": {
			Mode:        ModeDeny,
			DeniedPaths: []string{"/etc/**"},
		},
	})

	// Denied path.
	if err := c.CheckPath("read_file", "/etc/passwd"); err == nil {
		t.Error("expected deny for /etc/passwd")
	}

	// Non-denied path.
	if err := c.CheckPath("read_file", "/tmp/safe.txt"); err != nil {
		t.Errorf("expected allow for /tmp/safe.txt: %v", err)
	}
}

// --- CheckCommand ---

func TestCheckCommand_AllowAll(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"run_command": {Mode: ModeAllowAll},
	})
	if err := c.CheckCommand("run_command", "rm -rf /"); err != nil {
		t.Errorf("allow_all should permit any command: %v", err)
	}
}

func TestCheckCommand_Deny(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"run_command": {
			Mode:           ModeDeny,
			DeniedCommands: []string{"rm", "sudo"},
		},
	})

	if err := c.CheckCommand("run_command", "rm -rf /"); err == nil {
		t.Error("expected deny for rm")
	}
	if err := c.CheckCommand("run_command", "sudo apt install"); err == nil {
		t.Error("expected deny for sudo")
	}
	if err := c.CheckCommand("run_command", "ls -la"); err != nil {
		t.Errorf("expected allow for ls: %v", err)
	}
}

func TestCheckCommand_DenyWithPath(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"run_command": {
			Mode:           ModeDeny,
			DeniedCommands: []string{"rm"},
		},
	})

	// Full path to rm should still be blocked.
	if err := c.CheckCommand("run_command", "/bin/rm -rf /"); err == nil {
		t.Error("expected deny for /bin/rm")
	}
}

func TestCheckCommand_Whitelist(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"run_command": {
			Mode:            ModeWhitelist,
			AllowedCommands: []string{"ls", "cat", "echo"},
		},
	})

	if err := c.CheckCommand("run_command", "ls -la"); err != nil {
		t.Errorf("expected allow for ls: %v", err)
	}
	if err := c.CheckCommand("run_command", "cat /etc/hosts"); err != nil {
		t.Errorf("expected allow for cat: %v", err)
	}
	if err := c.CheckCommand("run_command", "rm file.txt"); err == nil {
		t.Error("expected deny for rm (not in whitelist)")
	}
}

func TestCheckCommand_EmptyInput(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"run_command": {Mode: ModeDeny, DeniedCommands: []string{"rm"}},
	})
	if err := c.CheckCommand("run_command", ""); err != nil {
		t.Errorf("empty command should be allowed: %v", err)
	}
}

// --- CheckDomain ---

func TestCheckDomain_AllowAll(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"web_fetch": {Mode: ModeAllowAll},
	})
	if err := c.CheckDomain("web_fetch", "https://evil.com/malware"); err != nil {
		t.Errorf("allow_all should permit any domain: %v", err)
	}
}

func TestCheckDomain_Deny(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"web_fetch": {
			Mode:          ModeDeny,
			DeniedDomains: []string{"evil.com", "*.malware.org"},
		},
	})

	if err := c.CheckDomain("web_fetch", "https://evil.com/page"); err == nil {
		t.Error("expected deny for evil.com")
	}
	if err := c.CheckDomain("web_fetch", "https://sub.malware.org/thing"); err == nil {
		t.Error("expected deny for sub.malware.org")
	}
	if err := c.CheckDomain("web_fetch", "https://safe.com"); err != nil {
		t.Errorf("expected allow for safe.com: %v", err)
	}
}

func TestCheckDomain_Whitelist(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"web_fetch": {
			Mode:           ModeWhitelist,
			AllowedDomains: []string{"*.google.com", "github.com"},
		},
	})

	if err := c.CheckDomain("web_fetch", "https://maps.google.com"); err != nil {
		t.Errorf("expected allow for maps.google.com: %v", err)
	}
	if err := c.CheckDomain("web_fetch", "https://github.com/repo"); err != nil {
		t.Errorf("expected allow for github.com: %v", err)
	}
	if err := c.CheckDomain("web_fetch", "https://evil.com"); err == nil {
		t.Error("expected deny for evil.com (not in whitelist)")
	}
}

func TestCheckDomain_WildcardMatchesRoot(t *testing.T) {
	c := NewChecker(map[string]Rule{
		"web_fetch": {
			Mode:           ModeWhitelist,
			AllowedDomains: []string{"*.google.com"},
		},
	})

	// *.google.com should also match google.com itself.
	if err := c.CheckDomain("web_fetch", "https://google.com"); err != nil {
		t.Errorf("expected *.google.com to match google.com: %v", err)
	}
}

// --- Helper functions ---

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/path", "example.com"},
		{"http://example.com:8080/path", "example.com"},
		{"example.com", "example.com"},
		{"https://sub.domain.org/", "sub.domain.org"},
	}

	for _, tt := range tests {
		got := extractDomain(tt.input)
		if got != tt.expected {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMatchDomain(t *testing.T) {
	tests := []struct {
		domain  string
		pattern string
		want    bool
	}{
		{"google.com", "google.com", true},
		{"evil.com", "google.com", false},
		{"maps.google.com", "*.google.com", true},
		{"google.com", "*.google.com", true},
		{"notgoogle.com", "*.google.com", false},
	}

	for _, tt := range tests {
		got := matchDomain(tt.domain, tt.pattern)
		if got != tt.want {
			t.Errorf("matchDomain(%q, %q) = %v, want %v", tt.domain, tt.pattern, got, tt.want)
		}
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	got := expandPath("~/Documents")
	expected := filepath.Join(home, "Documents")
	if got != expected {
		t.Errorf("expandPath(~/Documents) = %q, want %q", got, expected)
	}

	// Absolute path should stay absolute.
	got = expandPath("/etc/passwd")
	if got != "/etc/passwd" {
		t.Errorf("expandPath(/etc/passwd) = %q, want /etc/passwd", got)
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"/home/user/docs/file.txt", "/home/user/docs/**", true},
		{"/home/user/docs/sub/file.txt", "/home/user/docs/**", true},
		{"/etc/passwd", "/home/user/docs/**", false},
	}

	for _, tt := range tests {
		got := matchPath(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}
