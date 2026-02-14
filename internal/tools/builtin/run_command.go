// Package builtin provides built-in tools for AIDaemon.
package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunCommandTool executes shell commands.
type RunCommandTool struct {
	// BlockedCommands is a list of denied command names.
	// These are checked against every command in a pipeline.
	// Example: ["rm", "rmdir", "shred"]
	BlockedCommands []string

	// Timeout for command execution.
	Timeout time.Duration
}

func (t *RunCommandTool) Name() string {
	return "run_command"
}

func (t *RunCommandTool) Description() string {
	return "Execute a shell command and return its output. Commands are run in the user's home directory. Both stdout and stderr are captured."
}

func (t *RunCommandTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The shell command to execute (e.g. 'ls -la ~/Documents')",
			},
		},
		"required": []string{"command"},
	}
}

func (t *RunCommandTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdStr, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", fmt.Errorf("empty command")
	}

	// Strip leading shell comments (LLM sometimes sends "# comment\nactual_cmd").
	cmdStr = stripLeadingComments(cmdStr)
	if cmdStr == "" {
		return "", fmt.Errorf("empty command after stripping comments")
	}

	// Check ALL segments of the command (pipes, &&, ||, ;) for blocked commands.
	if blocked := t.findBlockedCommand(cmdStr); blocked != "" {
		return "", fmt.Errorf("access denied: command %q is blocked (destructive)", blocked)
	}

	// Set timeout.
	timeout := t.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Run through sh -c so shell features work: ~ expansion, pipes, redirects, etc.
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Combine stdout + stderr.
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n[stderr]\n" + stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("command failed: %w", err)
	}

	return output, nil
}

// findBlockedCommand checks all segments of a pipeline/chained command for blocked commands.
// Returns the blocked command name if found, or empty string if allowed.
func (t *RunCommandTool) findBlockedCommand(cmdStr string) string {
	if len(t.BlockedCommands) == 0 {
		return ""
	}

	// Split on shell operators: |, &&, ||, ;
	segments := splitCommandSegments(cmdStr)

	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		parts := parseCommand(seg)
		if len(parts) == 0 {
			continue
		}
		// Strip path: /bin/rm → rm
		baseName := parts[0]
		if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
			baseName = baseName[idx+1:]
		}
		for _, blocked := range t.BlockedCommands {
			if strings.EqualFold(baseName, blocked) {
				return baseName
			}
		}
	}

	return ""
}

// splitCommandSegments splits on shell operators: |, &&, ||, ;
func splitCommandSegments(s string) []string {
	var segments []string
	var current strings.Builder
	inQuote := false
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '"' || ch == '\'':
			inQuote = !inQuote
			current.WriteRune(ch)
		case inQuote:
			current.WriteRune(ch)
		case ch == '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
			segments = append(segments, current.String())
			current.Reset()
		case ch == '&' && i+1 < len(runes) && runes[i+1] == '&':
			i++
			segments = append(segments, current.String())
			current.Reset()
		case ch == ';':
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		segments = append(segments, current.String())
	}
	return segments
}

// stripLeadingComments removes lines starting with # from the beginning.
func stripLeadingComments(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// parseCommand splits a command string into parts.
// Handles simple quoting ("foo bar" stays as one arg).
func parseCommand(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false

	for _, ch := range s {
		switch ch {
		case ' ', '\t':
			if inQuote {
				current.WriteRune(ch)
			} else if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		case '"':
			inQuote = !inQuote
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}
