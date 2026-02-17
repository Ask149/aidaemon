package store

import (
	"fmt"
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
			ID:          fmt.Sprintf("cj_%c", 'a'+i),
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
