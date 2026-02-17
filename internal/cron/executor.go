package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Ask149/aidaemon/internal/store"
)

// CronSender delivers cron job output to a channel.
type CronSender interface {
	SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error
}

// Executor handles the actual execution of cron jobs.
type Executor struct {
	Sender CronSender
	// RunMessage sends a prompt through the LLM engine and returns the response.
	RunMessage func(ctx context.Context, prompt string) (string, error)
	// RunTool directly invokes a tool by name with JSON args.
	RunTool func(ctx context.Context, toolName, argsJSON string) (string, error)
}

// toolPayload is the JSON structure for tool-mode payloads.
type toolPayload struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// ExecuteJob runs a cron job and delivers the output.
func (e *Executor) ExecuteJob(ctx context.Context, job store.CronJob) (string, error) {
	var output string
	var err error

	switch job.Mode {
	case "message":
		if e.RunMessage == nil {
			return "", fmt.Errorf("message mode not configured")
		}
		output, err = e.RunMessage(ctx, job.Payload)

	case "tool":
		if e.RunTool == nil {
			return "", fmt.Errorf("tool mode not configured")
		}
		var tp toolPayload
		if jsonErr := json.Unmarshal([]byte(job.Payload), &tp); jsonErr != nil {
			return "", fmt.Errorf("invalid tool payload: %w", jsonErr)
		}
		output, err = e.RunTool(ctx, tp.Tool, string(tp.Args))

	default:
		return "", fmt.Errorf("unknown job mode: %s", job.Mode)
	}

	if err != nil {
		return "", err
	}

	// Deliver output to the source channel.
	if e.Sender != nil && output != "" {
		if sendErr := e.Sender.SendCronOutput(ctx, job.ChannelType, job.ChannelMeta, output); sendErr != nil {
			log.Printf("[cron] send output for job %s: %v", job.ID, sendErr)
		}
	}

	return output, nil
}
