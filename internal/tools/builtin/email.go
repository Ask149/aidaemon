package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// EmailTool provides Gmail access via the gmail.py script.
type EmailTool struct{}

func (t *EmailTool) Name() string {
	return "email"
}

func (t *EmailTool) Description() string {
	return "Access Gmail to read and search emails. " +
		"Use 'summary' for a quick overview of unread mail, 'list' to see recent emails, " +
		"'unread' to see only unread emails, 'read' to get the full content of a specific email by ID, " +
		"or 'search' to find emails matching a Gmail query string."
}

func (t *EmailTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"summary", "list", "unread", "search", "read"},
				"description": "The email action to perform.",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Gmail search query string (required for search). Supports Gmail search syntax, e.g. 'from:someone@example.com' or 'subject:invoice'.",
			},
			"message_id": map[string]interface{}{
				"type":        "string",
				"description": "The message ID of the email to read (required for read).",
			},
			"max_results": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of emails to return. Defaults to 10. Applies to list, unread, and search actions.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *EmailTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)

	switch action {
	case "summary":
		return t.summary(ctx)
	case "list":
		return t.list(ctx, args)
	case "unread":
		return t.unread(ctx, args)
	case "search":
		return t.search(ctx, args)
	case "read":
		return t.read(ctx, args)
	default:
		return "", fmt.Errorf("unknown action: %q (valid: summary, list, unread, search, read)", action)
	}
}

func (t *EmailTool) summary(ctx context.Context) (string, error) {
	return t.run(ctx, []string{"scripts/gmail.py", "summary"})
}

func (t *EmailTool) list(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdArgs := []string{"scripts/gmail.py", "list"}
	cmdArgs = appendMaxResults(cmdArgs, args)
	return t.run(ctx, cmdArgs)
}

func (t *EmailTool) unread(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdArgs := []string{"scripts/gmail.py", "unread"}
	cmdArgs = appendMaxResults(cmdArgs, args)
	return t.run(ctx, cmdArgs)
}

func (t *EmailTool) search(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required for search")
	}

	// query is a positional argument, placed right after the subcommand.
	cmdArgs := []string{"scripts/gmail.py", "search", query}
	cmdArgs = appendMaxResults(cmdArgs, args)
	return t.run(ctx, cmdArgs)
}

func (t *EmailTool) read(ctx context.Context, args map[string]interface{}) (string, error) {
	messageID, _ := args["message_id"].(string)
	if messageID == "" {
		return "", fmt.Errorf("message_id is required for read")
	}

	// message_id is a positional argument, placed right after the subcommand.
	cmdArgs := []string{"scripts/gmail.py", "read", messageID}
	return t.run(ctx, cmdArgs)
}

// appendMaxResults adds --max flag if max_results is provided in args.
func appendMaxResults(cmdArgs []string, args map[string]interface{}) []string {
	if m, ok := args["max_results"]; ok {
		var maxVal int
		switch v := m.(type) {
		case float64:
			maxVal = int(v)
		case string:
			parsed, err := strconv.Atoi(v)
			if err == nil {
				maxVal = parsed
			}
		}
		if maxVal > 0 {
			cmdArgs = append(cmdArgs, "--max", strconv.Itoa(maxVal))
		}
	}
	return cmdArgs
}

// run executes the gmail.py script with the given arguments.
func (t *EmailTool) run(ctx context.Context, scriptArgs []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3.11", scriptArgs...)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 0 {
			return string(output), fmt.Errorf("email command failed: %w", err)
		}
		return "", fmt.Errorf("email command failed: %w", err)
	}

	return string(output), nil
}
