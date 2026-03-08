package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/Ask149/aidaemon/internal/heartbeat"
)

// GoalsTool provides goal tracking via the heartbeat GoalsLog.
type GoalsTool struct {
	goalsLog *heartbeat.GoalsLog
}

// NewGoalsTool creates a GoalsTool backed by a JSONL file at goalsPath.
func NewGoalsTool(goalsPath string) *GoalsTool {
	return &GoalsTool{goalsLog: heartbeat.NewGoalsLog(goalsPath)}
}

func (t *GoalsTool) Name() string {
	return "goals"
}

func (t *GoalsTool) Description() string {
	return "Track personal goals like exercise, water intake, or meditation. " +
		"Use 'log' to record a completed goal entry, or 'progress' to check how you're doing against your target frequency."
}

func (t *GoalsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"log", "progress"},
				"description": "The goal action to perform.",
			},
			"goal": map[string]interface{}{
				"type":        "string",
				"description": "The goal name, e.g. 'exercise', 'water', 'meditation'.",
			},
			"note": map[string]interface{}{
				"type":        "string",
				"description": "What was done, e.g. '30 min run', '2 glasses'. Optional for log; defaults to 'completed'.",
			},
			"frequency": map[string]interface{}{
				"type":        "string",
				"description": "Target frequency like '3/week' or 'daily'. Optional for progress; defaults to '3/week'.",
			},
		},
		"required": []string{"action", "goal"},
	}
}

func (t *GoalsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)

	switch action {
	case "log":
		return t.log(args)
	case "progress":
		return t.progress(args)
	default:
		return "", fmt.Errorf("unknown action: %q (valid: log, progress)", action)
	}
}

func (t *GoalsTool) log(args map[string]interface{}) (string, error) {
	goal, _ := args["goal"].(string)
	if goal == "" {
		return "", fmt.Errorf("goal is required for log")
	}

	note, _ := args["note"].(string)
	if note == "" {
		note = "completed"
	}

	entry := heartbeat.GoalEntry{
		Date:    time.Now().Format("2006-01-02"),
		GoalID:  goal,
		Entry:   note,
		Counted: true,
	}

	if err := t.goalsLog.Record(entry); err != nil {
		return "", fmt.Errorf("record goal: %w", err)
	}

	return fmt.Sprintf("Logged goal '%s': %s", goal, note), nil
}

func (t *GoalsTool) progress(args map[string]interface{}) (string, error) {
	goal, _ := args["goal"].(string)
	if goal == "" {
		return "", fmt.Errorf("goal is required for progress")
	}

	frequency, _ := args["frequency"].(string)
	if frequency == "" {
		frequency = "3/week"
	}

	summary := t.goalsLog.ProgressSummary(goal, frequency)
	return fmt.Sprintf("Goal '%s' (%s): %s", goal, frequency, summary), nil
}
