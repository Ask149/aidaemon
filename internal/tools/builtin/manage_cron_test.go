package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Ask149/aidaemon/internal/store"
)

func newCronTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("newCronTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestManageCron_Create(t *testing.T) {
	st := newCronTestStore(t)
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
	st := newCronTestStore(t)
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
	st := newCronTestStore(t)
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
	st := newCronTestStore(t)
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
	st := newCronTestStore(t)
	tool := &ManageCronTool{Store: st}

	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "explode",
	})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestManageCron_InvalidCronExpr(t *testing.T) {
	st := newCronTestStore(t)
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
