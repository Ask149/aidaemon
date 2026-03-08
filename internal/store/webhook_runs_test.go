package store

import (
	"fmt"
	"testing"
	"time"
)

func TestCreateAndGetWebhookRun(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	run := WebhookRun{
		ID:          "wh_test1",
		Prompt:      "Review this alert",
		Payload:     `{"service":"api","status":"degraded"}`,
		Source:      "datadog",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":12345}`,
		Status:      "running",
		StartedAt:   now,
	}

	if err := s.CreateWebhookRun(run); err != nil {
		t.Fatalf("CreateWebhookRun: %v", err)
	}

	got, err := s.GetWebhookRun("wh_test1")
	if err != nil {
		t.Fatalf("GetWebhookRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetWebhookRun returned nil")
	}
	if got.Prompt != "Review this alert" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "Review this alert")
	}
	if got.Source != "datadog" {
		t.Errorf("Source = %q, want %q", got.Source, "datadog")
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want %q", got.Status, "running")
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt should be nil, got %v", got.FinishedAt)
	}
}

func TestGetWebhookRun_NotFound(t *testing.T) {
	s := newTestStore(t, 100)
	got, err := s.GetWebhookRun("nonexistent")
	if err != nil {
		t.Fatalf("GetWebhookRun: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestUpdateWebhookRun(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	s.CreateWebhookRun(WebhookRun{
		ID:          "wh_update",
		Prompt:      "test",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":123}`,
		Status:      "running",
		StartedAt:   now,
	})

	finished := now.Add(5 * time.Second)
	if err := s.UpdateWebhookRun("wh_update", "completed", "LLM response here", finished); err != nil {
		t.Fatalf("UpdateWebhookRun: %v", err)
	}

	got, _ := s.GetWebhookRun("wh_update")
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
	if got.Output != "LLM response here" {
		t.Errorf("Output = %q, want %q", got.Output, "LLM response here")
	}
	if got.FinishedAt == nil {
		t.Fatal("FinishedAt should not be nil")
	}
}

func TestListWebhookRuns(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	// Create 5 runs.
	for i := 0; i < 5; i++ {
		s.CreateWebhookRun(WebhookRun{
			ID:          fmt.Sprintf("wh_%d", i),
			Prompt:      fmt.Sprintf("prompt %d", i),
			ChannelType: "telegram",
			ChannelMeta: "{}",
			Status:      "completed",
			StartedAt:   now.Add(time.Duration(i) * time.Minute),
		})
	}

	// List with limit 3.
	runs, err := s.ListWebhookRuns(3, 0)
	if err != nil {
		t.Fatalf("ListWebhookRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(runs))
	}
	// Should be newest first.
	if runs[0].ID != "wh_4" {
		t.Errorf("first run should be wh_4, got %s", runs[0].ID)
	}

	// List with offset.
	runs2, _ := s.ListWebhookRuns(3, 3)
	if len(runs2) != 2 {
		t.Fatalf("expected 2 runs with offset 3, got %d", len(runs2))
	}
}
