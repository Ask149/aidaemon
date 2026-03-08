package heartbeat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GoalEntry represents a single goal check-in.
type GoalEntry struct {
	Date    string `json:"date"` // YYYY-MM-DD
	GoalID  string `json:"goal"`
	Entry   string `json:"entry"`
	Counted bool   `json:"counted"`
}

// GoalsLog manages goal tracking via a JSONL file.
type GoalsLog struct {
	path string
}

// NewGoalsLog creates a goals log at the given path.
func NewGoalsLog(path string) *GoalsLog {
	return &GoalsLog{path: path}
}

// Record appends a goal entry.
func (g *GoalsLog) Record(entry GoalEntry) error {
	if err := os.MkdirAll(filepath.Dir(g.path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	f, err := os.OpenFile(g.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open goals log: %w", err)
	}
	defer f.Close()

	data, _ := json.Marshal(entry)
	_, err = f.Write(append(data, '\n'))
	return err
}

// CountThisWeek counts entries for a goal in the current week (Monday-Sunday).
func (g *GoalsLog) CountThisWeek(goalID string) int {
	entries := g.readAll()
	weekStart := startOfWeek(time.Now())

	count := 0
	for _, e := range entries {
		if e.GoalID != goalID || !e.Counted {
			continue
		}
		entryDate, err := time.Parse("2006-01-02", e.Date)
		if err != nil {
			continue
		}
		if !entryDate.Before(weekStart) {
			count++
		}
	}
	return count
}

// CountToday counts entries for a goal today.
func (g *GoalsLog) CountToday(goalID string) int {
	entries := g.readAll()
	today := time.Now().Format("2006-01-02")

	count := 0
	for _, e := range entries {
		if e.GoalID == goalID && e.Counted && e.Date == today {
			count++
		}
	}
	return count
}

// ProgressSummary returns a human-readable progress string like "1/3 this week".
func (g *GoalsLog) ProgressSummary(goalID, frequency string) string {
	target, period := parseFrequency(frequency)
	if target == 0 {
		return "unknown frequency"
	}

	var count int
	switch period {
	case "week":
		count = g.CountThisWeek(goalID)
	case "day":
		count = g.CountToday(goalID)
	default:
		count = g.CountThisWeek(goalID)
		period = "week"
	}

	if count >= target {
		return fmt.Sprintf("✅ %d/%d this %s", count, target, period)
	}
	remaining := target - count
	return fmt.Sprintf("%d/%d this %s — %d more to go", count, target, period, remaining)
}

// parseFrequency parses "3/week" or "daily" into (target, period).
func parseFrequency(freq string) (int, string) {
	freq = strings.ToLower(strings.TrimSpace(freq))

	if freq == "daily" {
		return 1, "day"
	}

	parts := strings.SplitN(freq, "/", 2)
	if len(parts) != 2 {
		return 0, ""
	}

	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, ""
	}

	return n, parts[1]
}

// startOfWeek returns Monday 00:00:00 of the current week.
func startOfWeek(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday = 7
	}
	monday := t.AddDate(0, 0, -(weekday - 1))
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, t.Location())
}

func (g *GoalsLog) readAll() []GoalEntry {
	f, err := os.Open(g.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []GoalEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e GoalEntry
		if json.Unmarshal(scanner.Bytes(), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries
}
