package cron

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/store"
)

// newTestStore creates a temporary SQLite store for scheduler tests.
func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// mockExecutor records calls for testing.
type mockExecutor struct {
	mu    sync.Mutex
	calls []store.CronJob
}

func (m *mockExecutor) ExecuteJob(ctx context.Context, job store.CronJob) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, job)
	return "ok", nil
}

func (m *mockExecutor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func TestScheduler_FiresDueJobs(t *testing.T) {
	st := newTestStore(t)

	now := time.Now().Truncate(time.Second)
	past := now.Add(-5 * time.Minute)

	// Create a due job.
	st.CreateCronJob(store.CronJob{
		ID:          "cj_1",
		Label:       "Test job",
		CronExpr:    "* * * * *",
		Mode:        "message",
		Payload:     "hello",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":123}`,
		Enabled:     true,
		NextRunAt:   &past,
		CreatedAt:   now,
	})

	exec := &mockExecutor{}
	sched := NewScheduler(SchedulerConfig{
		Store:    st,
		Executor: exec,
		Interval: 100 * time.Millisecond, // fast tick for testing
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	sched.Start(ctx)
	<-ctx.Done()
	sched.Wait()

	if exec.CallCount() == 0 {
		t.Error("expected at least one job execution")
	}
}

func TestScheduler_SkipsDisabledJobs(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	past := now.Add(-5 * time.Minute)

	// Disabled job.
	st.CreateCronJob(store.CronJob{
		ID:          "cj_disabled",
		Label:       "Disabled",
		CronExpr:    "* * * * *",
		Mode:        "message",
		Payload:     "nope",
		ChannelType: "telegram",
		ChannelMeta: `{}`,
		Enabled:     false,
		NextRunAt:   &past,
		CreatedAt:   now,
	})

	exec := &mockExecutor{}
	sched := NewScheduler(SchedulerConfig{
		Store:    st,
		Executor: exec,
		Interval: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	sched.Start(ctx)
	<-ctx.Done()
	sched.Wait()

	if exec.CallCount() != 0 {
		t.Errorf("expected 0 executions for disabled job, got %d", exec.CallCount())
	}
}
