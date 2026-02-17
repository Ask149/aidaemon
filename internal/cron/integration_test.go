package cron

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/store"
)

func TestIntegration_CreateAndFireJob(t *testing.T) {
	// Setup store.
	st := newTestStore(t)

	// Track sent messages.
	var sentMu sync.Mutex
	var sentMessages []string

	sender := &testSender{
		fn: func(ctx context.Context, channelType, channelMeta, text string) error {
			sentMu.Lock()
			defer sentMu.Unlock()
			sentMessages = append(sentMessages, text)
			return nil
		},
	}

	exec := &Executor{
		Sender: sender,
		RunMessage: func(ctx context.Context, prompt string) (string, error) {
			return "Cron response to: " + prompt, nil
		},
	}

	// Create a job that's immediately due.
	now := time.Now()
	past := now.Add(-1 * time.Minute)
	st.CreateCronJob(store.CronJob{
		ID:          "cj_int_1",
		Label:       "Integration test job",
		CronExpr:    "* * * * *",
		Mode:        "message",
		Payload:     "What's the weather?",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":123}`,
		Enabled:     true,
		NextRunAt:   &past,
		CreatedAt:   now,
	})

	// Start scheduler with fast tick.
	sched := NewScheduler(SchedulerConfig{
		Store:    st,
		Executor: exec,
		Interval: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	sched.Start(ctx)
	<-ctx.Done()
	sched.Wait()

	// Verify job was fired and output sent.
	sentMu.Lock()
	defer sentMu.Unlock()
	if len(sentMessages) == 0 {
		t.Error("expected at least one sent message")
	}

	// Verify job's next_run_at was updated.
	job, _ := st.GetCronJob("cj_int_1")
	if job.LastRunAt == nil {
		t.Error("expected last_run_at to be set")
	}
	if job.NextRunAt == nil || !job.NextRunAt.After(now) {
		t.Error("expected next_run_at to be in the future")
	}
}

// testSender implements CronSender for testing.
type testSender struct {
	fn func(ctx context.Context, channelType, channelMeta, text string) error
}

func (s *testSender) SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error {
	return s.fn(ctx, channelType, channelMeta, text)
}
