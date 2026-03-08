// Package permissions provides a configurable permission system for tool execution.
//
// Permissions are defined per-tool in config.json under "tool_permissions".
// Three modes: "allow_all" (default), "whitelist", "deny".
//
// Path-based tools (read_file, write_file) use glob matching on paths.
// Command-based tools (run_command) use prefix matching on commands.
// Domain-based tools (web_fetch, browse_web) use suffix matching on domains.
package permissions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mode controls the permission enforcement strategy.
type Mode string

const (
	ModeAllowAll  Mode = "allow_all"  // No restrictions (default).
	ModeWhitelist Mode = "whitelist"  // Only explicitly allowed.
	ModeDeny      Mode = "deny"       // Everything except denied.
)

// Rule defines the permission rule for a single tool.
type Rule struct {
	Mode            Mode     `json:"mode"`
	AllowedPaths    []string `json:"allowed_paths,omitempty"`
	DeniedPaths     []string `json:"denied_paths,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	DeniedCommands  []string `json:"denied_commands,omitempty"`
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
	DeniedDomains   []string `json:"denied_domains,omitempty"`
}

// Checker evaluates permissions for tool calls.
type Checker struct {
	rules map[string]Rule
}

// NewChecker creates a permission checker from config rules.
// If rules is nil, all tools are allowed.
func NewChecker(rules map[string]Rule) *Checker {
	if rules == nil {
		rules = make(map[string]Rule)
	}
	return &Checker{rules: rules}
}

// CheckPath verifies a tool is allowed to access the given file path.
// Used for: read_file, write_file, read_text_file, etc.
func (c *Checker) CheckPath(toolName, path string) error {
	rule, ok := c.rules[toolName]
	if !ok {
		return nil // no rule = allow
	}

	expanded := expandPath(path)

	switch rule.Mode {
	case ModeAllowAll, "":
		return nil
	case ModeDeny:
		for _, pattern := range rule.DeniedPaths {
			if matchPath(expanded, expandPath(pattern)) {
				return fmt.Errorf("%s: access denied to %s", toolName, path)
			}
		}
		return nil
	case ModeWhitelist:
		for _, pattern := range rule.AllowedPaths {
			if matchPath(expanded, expandPath(pattern)) {
				return nil
			}
		}
		return fmt.Errorf("%s: path %s not in whitelist", toolName, path)
	}

	return nil
}

// CheckCommand verifies a tool is allowed to run the given command.
// Used for: run_command.
func (c *Checker) CheckCommand(toolName, command string) error {
	rule, ok := c.rules[toolName]
	if !ok {
		return nil
	}

	// Extract the base command (first word).
	base := strings.Fields(command)
	if len(base) == 0 {
		return nil
	}
	cmd := filepath.Base(base[0])

	switch rule.Mode {
	case ModeAllowAll, "":
		return nil
	case ModeDeny:
		for _, denied := range rule.DeniedCommands {
			if cmd == denied {
				return fmt.Errorf("%s: command %q is denied", toolName, cmd)
			}
		}
		return nil
	case ModeWhitelist:
		for _, allowed := range rule.AllowedCommands {
			if cmd == allowed {
				return nil
			}
		}
		return fmt.Errorf("%s: command %q not in whitelist", toolName, cmd)
	}

	return nil
}

// CheckDomain verifies a tool is allowed to access the given URL/domain.
// Used for: web_fetch, browse_web.
func (c *Checker) CheckDomain(toolName, rawURL string) error {
	rule, ok := c.rules[toolName]
	if !ok {
		return nil
	}

	domain := extractDomain(rawURL)
	if domain == "" {
		return nil
	}

	switch rule.Mode {
	case ModeAllowAll, "":
		return nil
	case ModeDeny:
		for _, denied := range rule.DeniedDomains {
			if matchDomain(domain, denied) {
				return fmt.Errorf("%s: domain %q is denied", toolName, domain)
			}
		}
		return nil
	case ModeWhitelist:
		for _, allowed := range rule.AllowedDomains {
			if matchDomain(domain, allowed) {
				return nil
			}
		}
		return fmt.Errorf("%s: domain %q not in whitelist", toolName, domain)
	}

	return nil
}

// --- Helpers ---

// expandPath expands ~ and resolves the path.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// matchPath checks if path matches a glob pattern.
// Supports ** for recursive matching.
func matchPath(path, pattern string) bool {
	// Handle ** suffix: /foo/** matches /foo/bar/baz.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return strings.HasPrefix(path, prefix)
	}
	// Try exact glob match.
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	return matched
}

// extractDomain pulls the domain from a URL.
func extractDomain(rawURL string) string {
	// Strip scheme.
	u := rawURL
	if idx := strings.Index(u, "://"); idx >= 0 {
		u = u[idx+3:]
	}
	// Strip path.
	if idx := strings.Index(u, "/"); idx >= 0 {
		u = u[:idx]
	}
	// Strip port.
	if idx := strings.LastIndex(u, ":"); idx >= 0 {
		u = u[:idx]
	}
	return strings.ToLower(u)
}

// matchDomain checks if domain matches a pattern.
// Supports wildcard prefix: *.google.com matches maps.google.com.
func matchDomain(domain, pattern string) bool {
	pattern = strings.ToLower(pattern)
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".google.com"
		return domain == pattern[2:] || strings.HasSuffix(domain, suffix)
	}
	return domain == pattern
}
