package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// CalendarTool provides Google Calendar access via the gcal.py script.
type CalendarTool struct{}

func (t *CalendarTool) Name() string {
	return "calendar"
}

func (t *CalendarTool) Description() string {
	return "Access Google Calendar to list upcoming events or create new ones. " +
		"Use 'list' to see scheduled events, 'create' to add a new event with title, start/end times, and optional details."
}

func (t *CalendarTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list", "create"},
				"description": "The calendar action to perform.",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Number of days to look ahead when listing events. Defaults to 1 (today only).",
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "Event title (required for create).",
			},
			"start": map[string]interface{}{
				"type":        "string",
				"description": "Event start time in ISO 8601 format, e.g. 2026-03-04T18:00:00 (required for create).",
			},
			"end": map[string]interface{}{
				"type":        "string",
				"description": "Event end time in ISO 8601 format, e.g. 2026-03-04T19:00:00 (required for create).",
			},
			"timezone": map[string]interface{}{
				"type":        "string",
				"description": "Timezone for the event, e.g. Asia/Kolkata. Defaults to Asia/Kolkata.",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Event description or notes (optional, for create).",
			},
			"location": map[string]interface{}{
				"type":        "string",
				"description": "Event location (optional, for create).",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CalendarTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)

	switch action {
	case "list":
		return t.list(ctx, args)
	case "create":
		return t.create(ctx, args)
	default:
		return "", fmt.Errorf("unknown action: %q (valid: list, create)", action)
	}
}

func (t *CalendarTool) list(ctx context.Context, args map[string]interface{}) (string, error) {
	days := 1 // Default: today only.
	if d, ok := args["days"]; ok {
		switch v := d.(type) {
		case float64:
			days = int(v)
		case string:
			parsed, err := strconv.Atoi(v)
			if err != nil {
				return "", fmt.Errorf("days must be an integer, got %q", v)
			}
			days = parsed
		}
	}

	cmdArgs := []string{"scripts/gcal.py", "list", "--days", strconv.Itoa(days)}
	return t.run(ctx, cmdArgs)
}

func (t *CalendarTool) create(ctx context.Context, args map[string]interface{}) (string, error) {
	title, _ := args["title"].(string)
	start, _ := args["start"].(string)
	end, _ := args["end"].(string)

	if title == "" {
		return "", fmt.Errorf("title is required for create")
	}
	if start == "" {
		return "", fmt.Errorf("start is required for create")
	}
	if end == "" {
		return "", fmt.Errorf("end is required for create")
	}

	tz, _ := args["timezone"].(string)
	if tz == "" {
		tz = "Asia/Kolkata" // Override script default (America/Los_Angeles) for IST.
	}

	cmdArgs := []string{
		"scripts/gcal.py", "create",
		"--title", title,
		"--start", start,
		"--end", end,
		"--tz", tz,
	}

	if desc, _ := args["description"].(string); desc != "" {
		cmdArgs = append(cmdArgs, "--desc", desc)
	}
	if loc, _ := args["location"].(string); loc != "" {
		cmdArgs = append(cmdArgs, "--location", loc)
	}

	return t.run(ctx, cmdArgs)
}

// run executes the gcal.py script with the given arguments.
func (t *CalendarTool) run(ctx context.Context, scriptArgs []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3.11", scriptArgs...)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 0 {
			return string(output), fmt.Errorf("calendar command failed: %w", err)
		}
		return "", fmt.Errorf("calendar command failed: %w", err)
	}

	return string(output), nil
}
