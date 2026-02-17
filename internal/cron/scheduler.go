package cron

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Ask149/aidaemon/internal/store"
)

// JobExecutor executes a cron job. Implemented by the executor component.
type JobExecutor interface {
	ExecuteJob(ctx context.Context, job store.CronJob) (output string, err error)
}

// SchedulerConfig holds dependencies for the scheduler.
type SchedulerConfig struct {
	Store    *store.SQLiteStore
	Executor JobExecutor
	Interval time.Duration // tick interval (default 60s)
}

// Scheduler checks for due cron jobs and fires them.
type Scheduler struct {
	store    *store.SQLiteStore
	executor JobExecutor
	interval time.Duration
	wg       sync.WaitGroup
}

// NewScheduler creates a new cron scheduler.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Scheduler{
		store:    cfg.Store,
		executor: cfg.Executor,
		interval: interval,
	}
}

// Start begins the scheduler tick loop. Non-blocking — runs in a goroutine.
func (s *Scheduler) Start(ctx context.Context) {
	s.wg.Add(1)
	go s.run(ctx)
	log.Printf("[cron] scheduler started (interval=%s)", s.interval)
}

// Wait blocks until all in-flight job executions complete.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) run(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Fire immediately on start, then on each tick.
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[cron] scheduler stopped")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now()
	jobs, err := s.store.DueCronJobs(now)
	if err != nil {
		log.Printf("[cron] query due jobs: %v", err)
		return
	}

	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		s.wg.Add(1)
		go s.fireJob(ctx, job)
	}
}

func (s *Scheduler) fireJob(ctx context.Context, job store.CronJob) {
	defer s.wg.Done()

	runID := "cr_" + job.ID + "_" + time.Now().Format("20060102150405")
	started := time.Now()

	log.Printf("[cron] firing job %s: %s", job.ID, job.Label)

	// Execute the job.
	output, err := s.executor.ExecuteJob(ctx, job)

	finished := time.Now()
	status := "success"
	if err != nil {
		status = "error"
		output = err.Error()
		log.Printf("[cron] job %s failed: %v", job.ID, err)
	} else {
		log.Printf("[cron] job %s completed in %s", job.ID, finished.Sub(started).Round(time.Millisecond))
	}

	// Record the run.
	s.store.CreateCronRun(store.CronRun{
		ID:         runID,
		JobID:      job.ID,
		StartedAt:  started,
		FinishedAt: &finished,
		Status:     status,
		Output:     truncateOutput(output, 10000),
	})

	// Compute next run time and update the job.
	sched, parseErr := Parse(job.CronExpr)
	if parseErr != nil {
		log.Printf("[cron] invalid cron expr for job %s: %v — disabling", job.ID, parseErr)
		job.Enabled = false
	} else {
		next := sched.Next(time.Now())
		job.NextRunAt = &next
	}
	job.LastRunAt = &finished
	s.store.UpdateCronJob(job)

	// Prune old runs.
	s.store.PruneCronRuns(job.ID, 50)
}

// truncateOutput limits output length for storage.
func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "... [truncated]"
}
