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
