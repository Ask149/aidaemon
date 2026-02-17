package builtin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Ask149/aidaemon/internal/cron"
	"github.com/Ask149/aidaemon/internal/store"
)

// ManageCronTool lets the agent manage cron/scheduled jobs.
type ManageCronTool struct {
	Store *store.SQLiteStore
	// ChannelType and ChannelMeta default channel context for new jobs.
	ChannelType string
	ChannelMeta string
}

func (t *ManageCronTool) Name() string {
	return "manage_cron"
}

func (t *ManageCronTool) Description() string {
	return "Manage scheduled/recurring tasks (cron jobs). Actions: create, list, pause, resume, delete. " +
		"When creating, translate the user's natural language schedule to a 5-field cron expression " +
		"(minute hour day-of-month month day-of-week). Day-of-week: 0=Sunday, 6=Saturday."
}

func (t *ManageCronTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"create", "list", "pause", "resume", "delete"},
				"description": "The action to perform.",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Human-readable description of the job (required for create).",
			},
			"cron_expr": map[string]interface{}{
				"type":        "string",
				"description": "5-field cron expression: minute hour day-of-month month day-of-week (required for create).",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"message", "tool"},
				"description": "Execution mode. 'message' sends a prompt to the LLM. 'tool' directly invokes a tool. Default: message.",
			},
			"payload": map[string]interface{}{
				"type":        "string",
				"description": "For message mode: the prompt text. For tool mode: JSON with 'tool' and 'args' keys (required for create).",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Job ID (required for pause/resume/delete).",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManageCronTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)

	switch action {
	case "create":
		return t.create(args)
	case "list":
		return t.list()
	case "pause":
		return t.setEnabled(args, false)
	case "resume":
		return t.setEnabled(args, true)
	case "delete":
		return t.delete(args)
	default:
		return "", fmt.Errorf("unknown action: %q (valid: create, list, pause, resume, delete)", action)
	}
}

func (t *ManageCronTool) create(args map[string]interface{}) (string, error) {
	label, _ := args["label"].(string)
	cronExpr, _ := args["cron_expr"].(string)
	mode, _ := args["mode"].(string)
	payload, _ := args["payload"].(string)

	if label == "" {
		return "", fmt.Errorf("label is required for create")
	}
	if cronExpr == "" {
		return "", fmt.Errorf("cron_expr is required for create")
	}
	if payload == "" {
		return "", fmt.Errorf("payload is required for create")
	}
	if mode == "" {
		mode = "message"
	}

	// Validate cron expression.
	sched, err := cron.Parse(cronExpr)
	if err != nil {
		return "", fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
	}

	// Compute next run time.
	next := sched.Next(time.Now())

	// Generate job ID.
	b := make([]byte, 6)
	rand.Read(b)
	id := "cj_" + hex.EncodeToString(b)

	// Use channel context from tool setup, or defaults.
	channelType := t.ChannelType
	channelMeta := t.ChannelMeta
	if channelType == "" {
		channelType = "unknown"
	}
	if channelMeta == "" {
		channelMeta = "{}"
	}

	job := store.CronJob{
		ID:          id,
		Label:       label,
		CronExpr:    cronExpr,
		Mode:        mode,
		Payload:     payload,
		ChannelType: channelType,
		ChannelMeta: channelMeta,
		Enabled:     true,
		NextRunAt:   &next,
		CreatedAt:   time.Now(),
	}

	if err := t.Store.CreateCronJob(job); err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}

	log.Printf("[manage_cron] created job %s: %s (next: %s)", id, label, next.Format("2006-01-02 15:04"))

	return fmt.Sprintf("Created scheduled job:\n- ID: %s\n- Schedule: %s\n- Next run: %s\n- Label: %s",
		id, cronExpr, next.Format("Mon Jan 2 15:04"), label), nil
}

func (t *ManageCronTool) list() (string, error) {
	jobs, err := t.Store.ListCronJobs()
	if err != nil {
		return "", err
	}

	// Return as JSON for the agent to format nicely.
	data, _ := json.MarshalIndent(jobs, "", "  ")
	return string(data), nil
}

func (t *ManageCronTool) setEnabled(args map[string]interface{}, enabled bool) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required for pause/resume")
	}

	job, err := t.Store.GetCronJob(id)
	if err != nil {
		return "", err
	}
	if job == nil {
		return "", fmt.Errorf("job %q not found", id)
	}

	job.Enabled = enabled

	// If resuming, recompute next run time.
	if enabled {
		sched, parseErr := cron.Parse(job.CronExpr)
		if parseErr == nil {
			next := sched.Next(time.Now())
			job.NextRunAt = &next
		}
	}

	if err := t.Store.UpdateCronJob(*job); err != nil {
		return "", err
	}

	action := "paused"
	if enabled {
		action = "resumed"
	}
	return fmt.Sprintf("Job %s %s: %s", id, action, job.Label), nil
}

func (t *ManageCronTool) delete(args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required for delete")
	}

	job, err := t.Store.GetCronJob(id)
	if err != nil {
		return "", err
	}
	if job == nil {
		return "", fmt.Errorf("job %q not found", id)
	}

	label := job.Label
	if err := t.Store.DeleteCronJob(id); err != nil {
		return "", err
	}

	return fmt.Sprintf("Deleted job %s: %s", id, label), nil
}
