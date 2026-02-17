package cron

import (
	"context"
	"testing"

	"github.com/Ask149/aidaemon/internal/store"
)

// mockSender records sent messages.
type mockSender struct {
	messages []string
}

func (m *mockSender) SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error {
	m.messages = append(m.messages, text)
	return nil
}

func TestExecutor_MessageMode(t *testing.T) {
	sender := &mockSender{}
	exec := &Executor{
		Sender: sender,
		RunMessage: func(ctx context.Context, prompt string) (string, error) {
			return "Response to: " + prompt, nil
		},
	}

	job := store.CronJob{
		ID:          "cj_test",
		Mode:        "message",
		Payload:     "Check my email",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":123}`,
	}

	output, err := exec.ExecuteJob(context.Background(), job)
	if err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if output != "Response to: Check my email" {
		t.Errorf("output = %q", output)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sender.messages))
	}
}

func TestExecutor_ToolMode(t *testing.T) {
	sender := &mockSender{}
	exec := &Executor{
		Sender: sender,
		RunTool: func(ctx context.Context, toolName, argsJSON string) (string, error) {
			return "tool result for " + toolName, nil
		},
	}

	job := store.CronJob{
		ID:          "cj_tool",
		Mode:        "tool",
		Payload:     `{"tool":"web_fetch","args":{"url":"https://example.com"}}`,
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":123}`,
	}

	output, err := exec.ExecuteJob(context.Background(), job)
	if err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if output != "tool result for web_fetch" {
		t.Errorf("output = %q", output)
	}
}

func TestExecutor_UnknownMode(t *testing.T) {
	exec := &Executor{}

	job := store.CronJob{
		ID:   "cj_bad",
		Mode: "explode",
	}

	_, err := exec.ExecuteJob(context.Background(), job)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
