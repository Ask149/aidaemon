# Cron / Scheduled Tasks Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add cron/scheduled task support — users describe recurring tasks in natural language via Telegram, the agent creates cron jobs, and a scheduler fires them on schedule.

**Architecture:** New `internal/cron/` package with a pure Go cron parser, a scheduler goroutine (ticks every 60s), and a job executor. SQLite tables for persistence (`cron_jobs`, `cron_runs`). A `manage_cron` built-in tool lets the agent create/list/pause/resume/delete jobs. HTTP API endpoints expose the same CRUD. Output is delivered back to the originating channel via a `CronSender` interface.

**Tech Stack:** Go stdlib + existing project deps (modernc.org/sqlite). No new external dependencies.

---

### Task 1: Cron expression parser with tests

**Files:**
- Create: `internal/cron/cron.go`
- Create: `internal/cron/cron_test.go`

**Step 1: Write failing tests for the cron parser**

Create `internal/cron/cron_test.go`:

```go
package cron

import (
	"testing"
	"time"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		expr string
	}{
		{"* * * * *"},          // every minute
		{"0 9 * * 1-5"},       // weekdays at 9am
		{"*/15 * * * *"},      // every 15 minutes
		{"0 0 1 * *"},         // first of month midnight
		{"30 8,12,18 * * *"},  // 8:30, 12:30, 18:30 daily
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			_, err := Parse(tc.expr)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.expr, err)
			}
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := []string{
		"",
		"* * *",           // too few fields
		"* * * * * *",     // too many fields
		"60 * * * *",      // minute out of range
		"* 25 * * *",      // hour out of range
		"* * 0 * *",       // day-of-month 0
		"* * * 13 *",      // month out of range
		"* * * * 8",       // day-of-week out of range
		"abc * * * *",     // non-numeric
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			_, err := Parse(expr)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", expr)
			}
		})
	}
}

func TestSchedule_Next(t *testing.T) {
	// "0 9 * * 1-5" = weekdays at 9:00 AM
	sched, err := Parse("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	// Monday 2026-02-16 08:00 → should be Monday 09:00
	from := time.Date(2026, 2, 16, 8, 0, 0, 0, time.Local)
	next := sched.Next(from)
	want := time.Date(2026, 2, 16, 9, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", from, next, want)
	}

	// Monday 2026-02-16 09:01 → should be Tuesday 09:00
	from2 := time.Date(2026, 2, 16, 9, 1, 0, 0, time.Local)
	next2 := sched.Next(from2)
	want2 := time.Date(2026, 2, 17, 9, 0, 0, 0, time.Local)
	if !next2.Equal(want2) {
		t.Errorf("Next(%v) = %v, want %v", from2, next2, want2)
	}

	// Friday 2026-02-20 10:00 → should skip weekend → Monday 09:00
	from3 := time.Date(2026, 2, 20, 10, 0, 0, 0, time.Local)
	next3 := sched.Next(from3)
	want3 := time.Date(2026, 2, 23, 9, 0, 0, 0, time.Local)
	if !next3.Equal(want3) {
		t.Errorf("Next(%v) = %v, want %v", from3, next3, want3)
	}
}

func TestSchedule_Next_EveryMinute(t *testing.T) {
	sched, err := Parse("* * * * *")
	if err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 1, 1, 12, 30, 45, 0, time.Local)
	next := sched.Next(from)
	want := time.Date(2026, 1, 1, 12, 31, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", from, next, want)
	}
}

func TestSchedule_Next_Step(t *testing.T) {
	// "*/15 * * * *" = every 15 minutes
	sched, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 1, 1, 12, 7, 0, 0, time.Local)
	next := sched.Next(from)
	want := time.Date(2026, 1, 1, 12, 15, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", from, next, want)
	}
}

func TestSchedule_Next_MonthRollover(t *testing.T) {
	// "0 0 1 * *" = first of every month at midnight
	sched, err := Parse("0 0 1 * *")
	if err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local)
	next := sched.Next(from)
	want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", from, next, want)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/cron/ -v`
Expected: FAIL (package doesn't exist)

**Step 3: Implement the cron parser**

Create `internal/cron/cron.go`:

```go
// Package cron provides a minimal cron expression parser and scheduler.
//
// Supports standard 5-field cron expressions:
//
//	minute hour day-of-month month day-of-week
//
// Field operators: * (any), , (list), - (range), / (step).
// Day-of-week: 0=Sunday, 6=Saturday.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule represents a parsed cron expression.
type Schedule struct {
	Minute     []bool // [0..59]
	Hour       []bool // [0..23]
	DayOfMonth []bool // [1..31] (index 0 unused)
	Month      []bool // [1..12] (index 0 unused)
	DayOfWeek  []bool // [0..6] (Sunday=0)
}

// Parse parses a standard 5-field cron expression.
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}

	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-month: %w", err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	dow, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-week: %w", err)
	}

	s := &Schedule{
		Minute:     make([]bool, 60),
		Hour:       make([]bool, 24),
		DayOfMonth: make([]bool, 32), // index 0 unused
		Month:      make([]bool, 13), // index 0 unused
		DayOfWeek:  make([]bool, 7),
	}
	for _, v := range minute {
		s.Minute[v] = true
	}
	for _, v := range hour {
		s.Hour[v] = true
	}
	for _, v := range dom {
		s.DayOfMonth[v] = true
	}
	for _, v := range month {
		s.Month[v] = true
	}
	for _, v := range dow {
		s.DayOfWeek[v] = true
	}

	return s, nil
}

// Next returns the next time after 'from' that matches the schedule.
// Searches up to 366 days ahead; returns zero time if no match found.
func (s *Schedule) Next(from time.Time) time.Time {
	// Start from the next minute.
	t := from.Truncate(time.Minute).Add(time.Minute)

	// Search up to 366 days × 1440 minutes = 527040 iterations max.
	// In practice, most schedules match within a few iterations.
	for i := 0; i < 527040; i++ {
		if s.Month[int(t.Month())] &&
			s.DayOfMonth[t.Day()] &&
			s.DayOfWeek[int(t.Weekday())] &&
			s.Hour[t.Hour()] &&
			s.Minute[t.Minute()] {
			return t
		}

		// Optimisation: skip to next valid month/day if current doesn't match.
		if !s.Month[int(t.Month())] {
			// Jump to first day of next month.
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.DayOfMonth[t.Day()] || !s.DayOfWeek[int(t.Weekday())] {
			// Jump to next day.
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.Hour[t.Hour()] {
			// Jump to next hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}

		t = t.Add(time.Minute)
	}

	return time.Time{} // no match within 366 days
}

// parseField parses a single cron field and returns the set of matching values.
func parseField(field string, min, max int) ([]int, error) {
	var values []int
	parts := strings.Split(field, ",")
	for _, part := range parts {
		vals, err := parsePart(part, min, max)
		if err != nil {
			return nil, err
		}
		values = append(values, vals...)
	}
	return values, nil
}

// parsePart handles a single element: *, N, N-M, */N, N-M/S.
func parsePart(part string, min, max int) ([]int, error) {
	// Handle step: "*/2", "1-5/2"
	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		var err error
		step, err = strconv.Atoi(part[idx+1:])
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step: %q", part)
		}
		part = part[:idx]
	}

	var lo, hi int
	if part == "*" {
		lo, hi = min, max
	} else if idx := strings.Index(part, "-"); idx >= 0 {
		var err error
		lo, err = strconv.Atoi(part[:idx])
		if err != nil {
			return nil, fmt.Errorf("invalid range start: %q", part)
		}
		hi, err = strconv.Atoi(part[idx+1:])
		if err != nil {
			return nil, fmt.Errorf("invalid range end: %q", part)
		}
	} else {
		var err error
		lo, err = strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid value: %q", part)
		}
		hi = lo
	}

	if lo < min || hi > max || lo > hi {
		return nil, fmt.Errorf("value out of range [%d-%d]: %d-%d", min, max, lo, hi)
	}

	var vals []int
	for v := lo; v <= hi; v += step {
		vals = append(vals, v)
	}
	return vals, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/cron/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/cron/cron.go internal/cron/cron_test.go
git commit -m "feat(cron): add 5-field cron expression parser with tests"
```

---

### Task 2: Store layer — cron_jobs and cron_runs tables

**Files:**
- Create: `internal/store/cron_jobs.go`
- Create: `internal/store/cron_jobs_test.go`
- Modify: `internal/store/store.go:54-99` (add cron methods to Conversation interface)
- Modify: `internal/store/store.go:154-176` (add migrateCronJobs call in migrate())

**Step 1: Add CronJob and CronRun structs + interface methods to store.go**

In `internal/store/store.go`, add after the `Message` struct (line 44) and before the `MessageWithID` struct (line 46):

```go
// CronJob represents a scheduled recurring task.
type CronJob struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	CronExpr    string     `json:"cron_expr"`
	Mode        string     `json:"mode"` // "message" or "tool"
	Payload     string     `json:"payload"`
	ChannelType string     `json:"channel_type"`
	ChannelMeta string     `json:"channel_meta"` // JSON
	Enabled     bool       `json:"enabled"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time `json:"next_run_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CronRun records a single execution of a cron job.
type CronRun struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Status     string     `json:"status"` // "success" or "error"
	Output     string     `json:"output"`
}
```

In the `Conversation` interface (line 56-99), add before `Close() error` (line 98):

```go
	// --- Cron jobs ---

	// CreateCronJob inserts a new cron job.
	CreateCronJob(job CronJob) error

	// GetCronJob returns a cron job by ID, or nil if not found.
	GetCronJob(id string) (*CronJob, error)

	// ListCronJobs returns all cron jobs.
	ListCronJobs() ([]CronJob, error)

	// UpdateCronJob updates a cron job (enabled, next_run_at, last_run_at).
	UpdateCronJob(job CronJob) error

	// DeleteCronJob removes a cron job and its run history.
	DeleteCronJob(id string) error

	// DueCronJobs returns enabled jobs whose next_run_at <= now.
	DueCronJobs(now time.Time) ([]CronJob, error)

	// CreateCronRun records a job execution.
	CreateCronRun(run CronRun) error

	// PruneCronRuns keeps only the most recent N runs per job.
	PruneCronRuns(jobID string, keep int) error
```

**Step 2: Write failing tests for cron job CRUD**

Create `internal/store/cron_jobs_test.go`:

```go
package store

import (
	"testing"
	"time"
)

func TestCreateAndGetCronJob(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)
	next := now.Add(1 * time.Hour)

	job := CronJob{
		ID:          "cj_test1",
		Label:       "Daily briefing",
		CronExpr:    "0 9 * * 1-5",
		Mode:        "message",
		Payload:     "Check my email",
		ChannelType: "telegram",
		ChannelMeta: `{"chat_id":12345}`,
		Enabled:     true,
		NextRunAt:   &next,
		CreatedAt:   now,
	}

	if err := s.CreateCronJob(job); err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	got, err := s.GetCronJob("cj_test1")
	if err != nil {
		t.Fatalf("GetCronJob: %v", err)
	}
	if got == nil {
		t.Fatal("GetCronJob returned nil")
	}
	if got.Label != "Daily briefing" {
		t.Errorf("Label = %q, want %q", got.Label, "Daily briefing")
	}
	if got.CronExpr != "0 9 * * 1-5" {
		t.Errorf("CronExpr = %q, want %q", got.CronExpr, "0 9 * * 1-5")
	}
	if !got.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestGetCronJob_NotFound(t *testing.T) {
	s := newTestStore(t, 100)
	got, err := s.GetCronJob("nonexistent")
	if err != nil {
		t.Fatalf("GetCronJob: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestListCronJobs(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	for i, label := range []string{"Job A", "Job B", "Job C"} {
		s.CreateCronJob(CronJob{
			ID:          "cj_" + string(rune('a'+i)),
			Label:       label,
			CronExpr:    "* * * * *",
			Mode:        "message",
			Payload:     "test",
			ChannelType: "telegram",
			ChannelMeta: "{}",
			Enabled:     true,
			CreatedAt:   now,
		})
	}

	jobs, err := s.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
}

func TestUpdateCronJob_PauseResume(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	s.CreateCronJob(CronJob{
		ID: "cj_pause", Label: "Test", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: true, CreatedAt: now,
	})

	job, _ := s.GetCronJob("cj_pause")
	job.Enabled = false
	if err := s.UpdateCronJob(*job); err != nil {
		t.Fatalf("UpdateCronJob: %v", err)
	}

	job, _ = s.GetCronJob("cj_pause")
	if job.Enabled {
		t.Error("expected Enabled=false after pause")
	}
}

func TestDeleteCronJob(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	s.CreateCronJob(CronJob{
		ID: "cj_del", Label: "Delete me", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: true, CreatedAt: now,
	})

	if err := s.DeleteCronJob("cj_del"); err != nil {
		t.Fatalf("DeleteCronJob: %v", err)
	}
	got, _ := s.GetCronJob("cj_del")
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestDueCronJobs(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	// Due job (next_run_at in the past, enabled).
	s.CreateCronJob(CronJob{
		ID: "cj_due", Label: "Due", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: true, NextRunAt: &past, CreatedAt: now,
	})

	// Not due (next_run_at in the future).
	s.CreateCronJob(CronJob{
		ID: "cj_notdue", Label: "Not due", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: true, NextRunAt: &future, CreatedAt: now,
	})

	// Disabled (should not be returned even though due).
	s.CreateCronJob(CronJob{
		ID: "cj_disabled", Label: "Disabled", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: false, NextRunAt: &past, CreatedAt: now,
	})

	due, err := s.DueCronJobs(now)
	if err != nil {
		t.Fatalf("DueCronJobs: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due job, got %d", len(due))
	}
	if due[0].ID != "cj_due" {
		t.Errorf("due job ID = %q, want cj_due", due[0].ID)
	}
}

func TestCreateCronRun(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	s.CreateCronJob(CronJob{
		ID: "cj_run", Label: "Test", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: true, CreatedAt: now,
	})

	finished := now.Add(5 * time.Second)
	run := CronRun{
		ID:         "cr_1",
		JobID:      "cj_run",
		StartedAt:  now,
		FinishedAt: &finished,
		Status:     "success",
		Output:     "All good",
	}

	if err := s.CreateCronRun(run); err != nil {
		t.Fatalf("CreateCronRun: %v", err)
	}
}

func TestPruneCronRuns(t *testing.T) {
	s := newTestStore(t, 100)
	now := time.Now().Truncate(time.Second)

	s.CreateCronJob(CronJob{
		ID: "cj_prune", Label: "Test", CronExpr: "* * * * *",
		Mode: "message", Payload: "test", ChannelType: "telegram",
		ChannelMeta: "{}", Enabled: true, CreatedAt: now,
	})

	// Create 5 runs.
	for i := 0; i < 5; i++ {
		started := now.Add(time.Duration(i) * time.Minute)
		s.CreateCronRun(CronRun{
			ID:        fmt.Sprintf("cr_%d", i),
			JobID:     "cj_prune",
			StartedAt: started,
			Status:    "success",
			Output:    fmt.Sprintf("Run %d", i),
		})
	}

	// Prune to keep 2.
	if err := s.PruneCronRuns("cj_prune", 2); err != nil {
		t.Fatalf("PruneCronRuns: %v", err)
	}

	// Verify only 2 remain.
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM cron_runs WHERE job_id = 'cj_prune'").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 runs after prune, got %d", count)
	}
}
```

Note: add `"fmt"` to the test imports.

**Step 2b: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestCreate.*Cron -v`
Expected: FAIL (methods not defined)

**Step 3: Add migrateCronJobs call in store.go migrate()**

In `internal/store/store.go`, update the `migrate()` method (line 154-176) to add cron migration after sessions:

After line 173 (`return err` for migrateSessions), add:

```go
	if err := s.migrateCronJobs(); err != nil {
		return err
	}
```

**Step 4: Implement cron_jobs.go**

Create `internal/store/cron_jobs.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// migrateCronJobs creates the cron_jobs and cron_runs tables.
func (s *SQLiteStore) migrateCronJobs() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS cron_jobs (
			id           TEXT PRIMARY KEY,
			label        TEXT NOT NULL,
			cron_expr    TEXT NOT NULL,
			mode         TEXT NOT NULL,
			payload      TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			channel_meta TEXT NOT NULL DEFAULT '{}',
			enabled      INTEGER NOT NULL DEFAULT 1,
			last_run_at  INTEGER,
			next_run_at  INTEGER,
			created_at   INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_cron_jobs_next_run
			ON cron_jobs(enabled, next_run_at);

		CREATE TABLE IF NOT EXISTS cron_runs (
			id          TEXT PRIMARY KEY,
			job_id      TEXT NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
			started_at  INTEGER NOT NULL,
			finished_at INTEGER,
			status      TEXT NOT NULL,
			output      TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_cron_runs_job
			ON cron_runs(job_id, started_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("migrate cron jobs: %w", err)
	}
	log.Printf("[store] cron_jobs table ready")
	return nil
}

// CreateCronJob inserts a new cron job.
func (s *SQLiteStore) CreateCronJob(job CronJob) error {
	enabled := 0
	if job.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO cron_jobs (id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		job.ID,
		job.Label,
		job.CronExpr,
		job.Mode,
		job.Payload,
		job.ChannelType,
		job.ChannelMeta,
		enabled,
		nilIfTime(job.LastRunAt),
		nilIfTime(job.NextRunAt),
		job.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("create cron job: %w", err)
	}
	log.Printf("[store] created cron job %s: %s", job.ID, job.Label)
	return nil
}

// GetCronJob returns a cron job by ID, or nil if not found.
func (s *SQLiteStore) GetCronJob(id string) (*CronJob, error) {
	var job CronJob
	var enabled int
	var lastRunAt, nextRunAt sql.NullInt64
	var createdAt int64

	err := s.db.QueryRow(`
		SELECT id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at
		FROM cron_jobs WHERE id = ?
	`, id).Scan(
		&job.ID, &job.Label, &job.CronExpr, &job.Mode, &job.Payload,
		&job.ChannelType, &job.ChannelMeta, &enabled,
		&lastRunAt, &nextRunAt, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cron job: %w", err)
	}

	job.Enabled = enabled == 1
	job.CreatedAt = time.Unix(createdAt, 0)
	if lastRunAt.Valid {
		t := time.Unix(lastRunAt.Int64, 0)
		job.LastRunAt = &t
	}
	if nextRunAt.Valid {
		t := time.Unix(nextRunAt.Int64, 0)
		job.NextRunAt = &t
	}

	return &job, nil
}

// ListCronJobs returns all cron jobs, newest first.
func (s *SQLiteStore) ListCronJobs() ([]CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at
		FROM cron_jobs
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list cron jobs: %w", err)
	}
	defer rows.Close()

	var jobs []CronJob
	for rows.Next() {
		var job CronJob
		var enabled int
		var lastRunAt, nextRunAt sql.NullInt64
		var createdAt int64

		err := rows.Scan(
			&job.ID, &job.Label, &job.CronExpr, &job.Mode, &job.Payload,
			&job.ChannelType, &job.ChannelMeta, &enabled,
			&lastRunAt, &nextRunAt, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan cron job: %w", err)
		}

		job.Enabled = enabled == 1
		job.CreatedAt = time.Unix(createdAt, 0)
		if lastRunAt.Valid {
			t := time.Unix(lastRunAt.Int64, 0)
			job.LastRunAt = &t
		}
		if nextRunAt.Valid {
			t := time.Unix(nextRunAt.Int64, 0)
			job.NextRunAt = &t
		}

		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cron jobs: %w", err)
	}
	if jobs == nil {
		jobs = []CronJob{}
	}
	return jobs, nil
}

// UpdateCronJob updates a cron job.
func (s *SQLiteStore) UpdateCronJob(job CronJob) error {
	enabled := 0
	if job.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		UPDATE cron_jobs
		SET label = ?, cron_expr = ?, mode = ?, payload = ?, enabled = ?, last_run_at = ?, next_run_at = ?
		WHERE id = ?
	`,
		job.Label, job.CronExpr, job.Mode, job.Payload, enabled,
		nilIfTime(job.LastRunAt), nilIfTime(job.NextRunAt),
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("update cron job: %w", err)
	}
	return nil
}

// DeleteCronJob removes a cron job and its run history.
func (s *SQLiteStore) DeleteCronJob(id string) error {
	// Runs are deleted by CASCADE, but be explicit.
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM cron_runs WHERE job_id = ?", id); err != nil {
		return fmt.Errorf("delete cron runs: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM cron_jobs WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete cron job: %w", err)
	}
	return tx.Commit()
}

// DueCronJobs returns enabled jobs whose next_run_at <= now.
func (s *SQLiteStore) DueCronJobs(now time.Time) ([]CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at
		FROM cron_jobs
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC
	`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("due cron jobs: %w", err)
	}
	defer rows.Close()

	var jobs []CronJob
	for rows.Next() {
		var job CronJob
		var enabled int
		var lastRunAt, nextRunAt sql.NullInt64
		var createdAt int64

		err := rows.Scan(
			&job.ID, &job.Label, &job.CronExpr, &job.Mode, &job.Payload,
			&job.ChannelType, &job.ChannelMeta, &enabled,
			&lastRunAt, &nextRunAt, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan due cron job: %w", err)
		}

		job.Enabled = enabled == 1
		job.CreatedAt = time.Unix(createdAt, 0)
		if lastRunAt.Valid {
			t := time.Unix(lastRunAt.Int64, 0)
			job.LastRunAt = &t
		}
		if nextRunAt.Valid {
			t := time.Unix(nextRunAt.Int64, 0)
			job.NextRunAt = &t
		}

		jobs = append(jobs, job)
	}
	return jobs, nil
}

// CreateCronRun records a job execution.
func (s *SQLiteStore) CreateCronRun(run CronRun) error {
	_, err := s.db.Exec(`
		INSERT INTO cron_runs (id, job_id, started_at, finished_at, status, output)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.JobID,
		run.StartedAt.Unix(),
		nilIfTime(run.FinishedAt),
		run.Status,
		run.Output,
	)
	if err != nil {
		return fmt.Errorf("create cron run: %w", err)
	}
	return nil
}

// PruneCronRuns keeps only the most recent N runs for a job.
func (s *SQLiteStore) PruneCronRuns(jobID string, keep int) error {
	_, err := s.db.Exec(`
		DELETE FROM cron_runs
		WHERE job_id = ? AND id NOT IN (
			SELECT id FROM cron_runs
			WHERE job_id = ?
			ORDER BY started_at DESC
			LIMIT ?
		)
	`, jobID, jobID, keep)
	if err != nil {
		return fmt.Errorf("prune cron runs: %w", err)
	}
	return nil
}
```

**Step 5: Run all store tests**

Run: `go test ./internal/store/ -v`
Expected: ALL PASS

**Step 6: Commit**

```bash
git add internal/store/store.go internal/store/cron_jobs.go internal/store/cron_jobs_test.go
git commit -m "feat(store): add cron_jobs and cron_runs tables with CRUD"
```

---

### Task 3: Scheduler goroutine

**Files:**
- Create: `internal/cron/scheduler.go`
- Create: `internal/cron/scheduler_test.go`

**Step 1: Write failing tests**

Create `internal/cron/scheduler_test.go`:

```go
package cron

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Ask149/aidaemon/internal/store"
)

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
	// Create a real SQLite store.
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
```

Add a test helper at the top of the file:

```go
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
```

Add `"path/filepath"` to imports.

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/cron/ -run TestScheduler -v`
Expected: FAIL (NewScheduler not defined)

**Step 3: Implement the scheduler**

Create `internal/cron/scheduler.go`:

```go
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

	// Record the run start.
	s.store.CreateCronRun(store.CronRun{
		ID:        runID,
		JobID:     job.ID,
		StartedAt: started,
		Status:    "running",
	})

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

	// Update the run record.
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
```

Note: The `CreateCronRun` for the initial "running" state will get a UNIQUE constraint error on the second call with the same ID. Fix this by using `INSERT OR REPLACE` in the store, OR use different IDs. Simpler: just update the run at the end. Let me revise — we'll just create the run record once at the end with the final status. Remove the first `CreateCronRun` call and only call it at the end.

Actually, simpler: use `INSERT OR REPLACE` in `CreateCronRun` in the store layer. Update the SQL in `cron_jobs.go` to use `INSERT OR REPLACE INTO cron_runs`.

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/cron/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/cron/scheduler.go internal/cron/scheduler_test.go
git commit -m "feat(cron): add scheduler goroutine with tick loop"
```

---

### Task 4: Job executor — message and tool modes

**Files:**
- Create: `internal/cron/executor.go`
- Create: `internal/cron/executor_test.go`

**Step 1: Write failing tests**

Create `internal/cron/executor_test.go`:

```go
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
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/cron/ -run TestExecutor -v`
Expected: FAIL (Executor not defined)

**Step 3: Implement the executor**

Create `internal/cron/executor.go`:

```go
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
	Sender     CronSender
	// RunMessage sends a prompt through the LLM engine and returns the response.
	RunMessage func(ctx context.Context, prompt string) (string, error)
	// RunTool directly invokes a tool by name with JSON args.
	RunTool    func(ctx context.Context, toolName, argsJSON string) (string, error)
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
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/cron/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/cron/executor.go internal/cron/executor_test.go
git commit -m "feat(cron): add job executor with message and tool modes"
```

---

### Task 5: manage_cron agent tool

**Files:**
- Create: `internal/tools/builtin/manage_cron.go`
- Create: `internal/tools/builtin/manage_cron_test.go`

**Step 1: Write failing tests**

Create `internal/tools/builtin/manage_cron_test.go`:

```go
package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Ask149/aidaemon/internal/store"
)

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

func TestManageCron_Create(t *testing.T) {
	st := newTestStore(t)
	tool := &ManageCronTool{Store: st}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":    "create",
		"label":     "Morning briefing",
		"cron_expr": "0 9 * * 1-5",
		"mode":      "message",
		"payload":   "Check my email and calendar",
	})
	if err != nil {
		t.Fatalf("Execute create: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Verify job was created.
	jobs, _ := st.ListCronJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Label != "Morning briefing" {
		t.Errorf("label = %q", jobs[0].Label)
	}
}

func TestManageCron_List(t *testing.T) {
	st := newTestStore(t)
	tool := &ManageCronTool{Store: st}

	// Create a job first.
	tool.Execute(context.Background(), map[string]interface{}{
		"action": "create", "label": "Test", "cron_expr": "* * * * *",
		"mode": "message", "payload": "test",
	})

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("Execute list: %v", err)
	}

	// Result should be valid JSON array.
	var jobs []interface{}
	if err := json.Unmarshal([]byte(result), &jobs); err != nil {
		t.Fatalf("list result not valid JSON: %v\nresult: %s", err, result)
	}
	if len(jobs) != 1 {
		t.Errorf("expected 1 job in list, got %d", len(jobs))
	}
}

func TestManageCron_PauseResume(t *testing.T) {
	st := newTestStore(t)
	tool := &ManageCronTool{Store: st}

	tool.Execute(context.Background(), map[string]interface{}{
		"action": "create", "label": "Test", "cron_expr": "* * * * *",
		"mode": "message", "payload": "test",
	})

	jobs, _ := st.ListCronJobs()
	jobID := jobs[0].ID

	// Pause.
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "pause",
		"id":     jobID,
	})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}

	job, _ := st.GetCronJob(jobID)
	if job.Enabled {
		t.Error("expected disabled after pause")
	}

	// Resume.
	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "resume",
		"id":     jobID,
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	job, _ = st.GetCronJob(jobID)
	if !job.Enabled {
		t.Error("expected enabled after resume")
	}
}

func TestManageCron_Delete(t *testing.T) {
	st := newTestStore(t)
	tool := &ManageCronTool{Store: st}

	tool.Execute(context.Background(), map[string]interface{}{
		"action": "create", "label": "Delete me", "cron_expr": "* * * * *",
		"mode": "message", "payload": "test",
	})

	jobs, _ := st.ListCronJobs()
	jobID := jobs[0].ID

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "delete",
		"id":     jobID,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	remaining, _ := st.ListCronJobs()
	if len(remaining) != 0 {
		t.Errorf("expected 0 jobs after delete, got %d", len(remaining))
	}
}

func TestManageCron_InvalidAction(t *testing.T) {
	st := newTestStore(t)
	tool := &ManageCronTool{Store: st}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "explode",
	})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestManageCron_InvalidCronExpr(t *testing.T) {
	st := newTestStore(t)
	tool := &ManageCronTool{Store: st}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":    "create",
		"label":     "Bad",
		"cron_expr": "not a cron",
		"mode":      "message",
		"payload":   "test",
	})
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/tools/builtin/ -run TestManageCron -v`
Expected: FAIL (ManageCronTool not defined)

**Step 3: Implement the manage_cron tool**

Create `internal/tools/builtin/manage_cron.go`:

```go
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
	Store       *store.SQLiteStore
	// ChannelType and ChannelMeta are set per-call by the channel layer
	// to auto-capture the source channel context.
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
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/tools/builtin/ -run TestManageCron -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add internal/tools/builtin/manage_cron.go internal/tools/builtin/manage_cron_test.go
git commit -m "feat(tools): add manage_cron built-in tool for agent CRUD"
```

---

### Task 6: HTTP API endpoints for cron jobs

**Files:**
- Modify: `internal/httpapi/httpapi.go:37-53` (add Store field type widening or cron-specific methods)
- Modify: `internal/httpapi/httpapi.go:72-89` (register cron routes)
- Modify: `internal/httpapi/httpapi.go` (add handler methods)

**Step 1: Add cron routes and handlers**

In `internal/httpapi/httpapi.go`, add to the route registration block (after line 80):

```go
	mux.HandleFunc("GET /cron/jobs", api.requireAuth(api.handleListCronJobs))
	mux.HandleFunc("POST /cron/jobs", api.requireAuth(api.handleCreateCronJob))
	mux.HandleFunc("PATCH /cron/jobs/{id}", api.requireAuth(api.handleUpdateCronJob))
	mux.HandleFunc("DELETE /cron/jobs/{id}", api.requireAuth(api.handleDeleteCronJob))
```

Add handler implementations at the end of the file (before the closing brace of the file):

```go
// --- Cron job handlers ---

func (a *API) handleListCronJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := a.cfg.Store.ListCronJobs()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, jobs)
}

type createCronJobRequest struct {
	Label    string `json:"label"`
	CronExpr string `json:"cron_expr"`
	Mode     string `json:"mode"`
	Payload  string `json:"payload"`
}

func (a *API) handleCreateCronJob(w http.ResponseWriter, r *http.Request) {
	var req createCronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Label == "" || req.CronExpr == "" || req.Payload == "" {
		jsonError(w, http.StatusBadRequest, "label, cron_expr, and payload are required")
		return
	}
	if req.Mode == "" {
		req.Mode = "message"
	}

	// Validate cron expression.
	sched, err := cron.Parse(req.CronExpr)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid cron expression: "+err.Error())
		return
	}

	next := sched.Next(time.Now())
	b := make([]byte, 6)
	rand.Read(b)
	id := "cj_" + hex.EncodeToString(b)

	job := store.CronJob{
		ID:          id,
		Label:       req.Label,
		CronExpr:    req.CronExpr,
		Mode:        req.Mode,
		Payload:     req.Payload,
		ChannelType: "http",
		ChannelMeta: "{}",
		Enabled:     true,
		NextRunAt:   &next,
		CreatedAt:   time.Now(),
	}

	if err := a.cfg.Store.CreateCronJob(job); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResp(w, http.StatusCreated, job)
}

type updateCronJobRequest struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Label   string `json:"label,omitempty"`
}

func (a *API) handleUpdateCronJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "job id is required")
		return
	}

	var req updateCronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	job, err := a.cfg.Store.GetCronJob(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job == nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}

	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	if req.Label != "" {
		job.Label = req.Label
	}

	if err := a.cfg.Store.UpdateCronJob(*job); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResp(w, http.StatusOK, job)
}

func (a *API) handleDeleteCronJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "job id is required")
		return
	}

	if err := a.cfg.Store.DeleteCronJob(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}
```

Add imports to `httpapi.go`: `"crypto/rand"`, `"encoding/hex"`, and `"github.com/Ask149/aidaemon/internal/cron"`.

**Step 2: Build to verify compilation**

Run: `go build ./...`
Expected: SUCCESS

**Step 3: Commit**

```bash
git add internal/httpapi/httpapi.go
git commit -m "feat(httpapi): add cron job CRUD endpoints"
```

---

### Task 7: Wire everything in main.go

**Files:**
- Modify: `cmd/aidaemon/main.go` (add scheduler startup, manage_cron tool registration, CronSender)

**Step 1: Add Telegram CronSender**

Create `internal/cron/sender.go`:

```go
package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
)

// TelegramSender sends cron output to Telegram chats.
type TelegramSender struct {
	// SendFn sends a message to a Telegram chat.
	// Takes (ctx, chatID, text) and returns error.
	SendFn func(ctx context.Context, chatID int64, text string) error
}

// SendCronOutput sends output to the appropriate channel.
func (s *TelegramSender) SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error {
	switch channelType {
	case "telegram":
		var meta struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := json.Unmarshal([]byte(channelMeta), &meta); err != nil {
			return fmt.Errorf("parse telegram meta: %w", err)
		}
		if meta.ChatID == 0 {
			// Try string format.
			var metaStr struct {
				ChatID string `json:"chat_id"`
			}
			json.Unmarshal([]byte(channelMeta), &metaStr)
			meta.ChatID, _ = strconv.ParseInt(metaStr.ChatID, 10, 64)
		}
		if meta.ChatID == 0 {
			return fmt.Errorf("telegram meta missing chat_id")
		}
		return s.SendFn(ctx, meta.ChatID, text)

	default:
		log.Printf("[cron] unsupported channel type %q — output stored in run history only", channelType)
		return nil
	}
}
```

**Step 2: Wire in main.go**

In `cmd/aidaemon/main.go`, add import `"github.com/Ask149/aidaemon/internal/cron"`.

After the session manager creation (line 191) and before the services section (line 197), the manage_cron tool needs to be registered. However, `setupTools` runs before the store is created (line 103 vs 156). Two options:

**Option A:** Register `manage_cron` after the store is created, directly on the registry.

After line 195 (`log.Printf("[daemon] existing sessions migrated")`), add:

```go
	// Register cron management tool (needs store, registered after setupTools).
	registry.Register(&builtin.ManageCronTool{
		Store: st,
	})
```

After the Telegram bot is started (line 283), add the scheduler startup:

```go
	// 8. Cron scheduler.
	var cronSender cron.CronSender
	if tbot != nil {
		cronSender = &cron.TelegramSender{
			SendFn: func(ctx context.Context, chatID int64, text string) error {
				sid := channel.SessionID("telegram", strconv.FormatInt(chatID, 10))
				return tbot.Send(ctx, sid, text)
			},
		}
	}

	cronExec := &cron.Executor{
		Sender: cronSender,
		RunMessage: func(ctx context.Context, prompt string) (string, error) {
			sysPrompt := workspace.Load(wsDir, skillsDir).SystemPrompt()
			messages := []provider.Message{
				{Role: "system", Content: sysPrompt},
				{Role: "user", Content: prompt},
			}
			result, err := mgr.Engine().Run(ctx, messages, engine.RunOptions{
				Model:         cfg.ChatModel,
				MaxIterations: 25,
			})
			if err != nil {
				if result != nil && result.Content != "" {
					return result.Content, nil
				}
				return "", err
			}
			return result.Content, nil
		},
		RunTool: func(ctx context.Context, toolName, argsJSON string) (string, error) {
			return registry.Execute(ctx, toolName, argsJSON)
		},
	}

	cronScheduler := cron.NewScheduler(cron.SchedulerConfig{
		Store:    st,
		Executor: cronExec,
	})
	cronScheduler.Start(ctx)
	log.Printf("[daemon] cron scheduler started")
```

Note: The `RunMessage` func above uses `mgr.Engine()` — but the Manager doesn't expose the engine. We need to either:
1. Create a new engine instance for cron (simple, avoids coupling)
2. Expose `Engine()` on Manager

**Simpler approach:** Create a dedicated engine for cron runs:

```go
	cronEngine := &engine.Engine{
		Provider: prov,
		Registry: registry,
	}
```

And use that in `RunMessage`:

```go
	RunMessage: func(ctx context.Context, prompt string) (string, error) {
		sysPrompt := workspace.Load(wsDir, skillsDir).SystemPrompt()
		messages := []provider.Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		}
		result, err := cronEngine.Run(ctx, messages, engine.RunOptions{
			Model:         cfg.ChatModel,
			MaxIterations: 25,
		})
		if err != nil {
			if result != nil && result.Content != "" {
				return result.Content, nil
			}
			return "", err
		}
		return result.Content, nil
	},
```

Also need to set the `ChannelType` and `ChannelMeta` on the manage_cron tool dynamically per call. The simplest approach: the Telegram bot sets these before each call. But tools are shared across the registry — we can't mutate a shared tool per call.

**Better approach:** The manage_cron tool reads channel context from a well-known key in the args map, set by the channel layer. Or, simpler for v1: the agent always specifies the channel info since we've only got Telegram. OR — set defaults on the tool based on the primary channel:

```go
	cronTool := &builtin.ManageCronTool{
		Store:       st,
		ChannelType: "telegram",
		ChannelMeta: fmt.Sprintf(`{"chat_id":%d}`, cfg.TelegramUserID),
	}
	registry.Register(cronTool)
```

This works for v1 since Ashish is the single user with one Telegram chat.

**Step 3: Add imports to main.go**

Add to imports:
- `"github.com/Ask149/aidaemon/internal/cron"`
- Already has: `"github.com/Ask149/aidaemon/internal/provider"`

**Step 4: Build and run all tests**

Run: `go build ./... && go test ./... -count=1`
Expected: Build succeeds, all tests pass.

**Step 5: Commit**

```bash
git add cmd/aidaemon/main.go internal/cron/sender.go
git commit -m "feat: wire cron scheduler, executor, and manage_cron tool in main"
```

---

### Task 8: Integration test — end-to-end cron flow

**Files:**
- Create: `internal/cron/integration_test.go`

**Step 1: Write integration test**

```go
package cron

import (
	"context"
	"path/filepath"
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
```

**Step 2: Run integration test**

Run: `go test ./internal/cron/ -run TestIntegration -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/cron/integration_test.go
git commit -m "test(cron): add end-to-end integration test"
```

---

### Task 9: Full build and test verification

**Step 1: Run full test suite**

Run: `go test ./... -count=1 -race`
Expected: ALL PASS (14+ packages)

**Step 2: Build the binary**

Run: `go build -o aidaemon ./cmd/aidaemon/`
Expected: SUCCESS

**Step 3: Commit any final fixes**

If any tests fail or build issues arise, fix and commit.

---

### Task 10: Update documentation

**Files:**
- Modify: `README.md` (add cron/scheduled tasks section)
- Modify: `CHANGELOG.md` (add v2.1.0 entry)

**Step 1: Add cron documentation to README**

Add a "Scheduled Tasks (Cron)" section after the Skill Files section explaining:
- How to create scheduled tasks via Telegram natural language
- Example: "Every weekday at 9am, check my email and give me a briefing"
- How to list, pause, resume, delete via conversation
- HTTP API endpoints for programmatic access
- Supported cron expression format

**Step 2: Update CHANGELOG**

Add entry for the cron feature under a new version or Unreleased section.

**Step 3: Commit**

```bash
git add README.md CHANGELOG.md
git commit -m "docs: add scheduled tasks documentation"
```
