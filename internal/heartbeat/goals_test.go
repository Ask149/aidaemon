package heartbeat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGoalsLog_RecordAndCount(t *testing.T) {
	dir := t.TempDir()
	log := NewGoalsLog(filepath.Join(dir, "goals-log.jsonl"))

	// Record two exercise entries this week.
	log.Record(GoalEntry{
		Date:    time.Now().Format("2006-01-02"),
		GoalID:  "exercise",
		Entry:   "ran 5k",
		Counted: true,
	})
	log.Record(GoalEntry{
		Date:    time.Now().Format("2006-01-02"),
		GoalID:  "exercise",
		Entry:   "gym session",
		Counted: true,
	})

	count := log.CountThisWeek("exercise")
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestGoalsLog_CountThisWeek_IgnoresOldEntries(t *testing.T) {
	dir := t.TempDir()
	log := NewGoalsLog(filepath.Join(dir, "goals-log.jsonl"))

	// Record entry from 10 days ago.
	oldDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	log.Record(GoalEntry{
		Date:    oldDate,
		GoalID:  "exercise",
		Entry:   "old run",
		Counted: true,
	})

	count := log.CountThisWeek("exercise")
	if count != 0 {
		t.Errorf("count = %d, want 0 (old entry)", count)
	}
}

func TestGoalsLog_ProgressSummary(t *testing.T) {
	dir := t.TempDir()
	log := NewGoalsLog(filepath.Join(dir, "goals-log.jsonl"))

	log.Record(GoalEntry{
		Date:    time.Now().Format("2006-01-02"),
		GoalID:  "exercise",
		Entry:   "ran 5k",
		Counted: true,
	})

	summary := log.ProgressSummary("exercise", "3/week")
	// Should contain "1/3" somewhere.
	if summary == "" {
		t.Error("summary should not be empty")
	}
}
