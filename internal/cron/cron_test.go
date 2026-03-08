package cron

import (
	"testing"
	"time"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		expr string
	}{
		{"* * * * *"},        // every minute
		{"0 9 * * 1-5"},      // weekdays at 9am
		{"*/15 * * * *"},     // every 15 minutes
		{"0 0 1 * *"},        // first of month midnight
		{"30 8,12,18 * * *"}, // 8:30, 12:30, 18:30 daily
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
		"* * *",       // too few fields
		"* * * * * *", // too many fields
		"60 * * * *",  // minute out of range
		"* 25 * * *",  // hour out of range
		"* * 0 * *",   // day-of-month 0
		"* * * 13 *",  // month out of range
		"* * * * 8",   // day-of-week out of range
		"abc * * * *", // non-numeric
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
