package main

import (
	"strings"
	"testing"
	"time"
)

func occurrence(summary string, allDay bool, start, end time.Time, category string) Occurrence {
	return Occurrence{Summary: summary, AllDay: allDay, Start: start, End: end, Category: category}
}

func TestWeekViewGroupsAndSorts(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, loc)
	occs := []Occurrence{
		occurrence("Later", false,
			time.Date(2026, 10, 20, 15, 0, 0, 0, loc),
			time.Date(2026, 10, 20, 16, 0, 0, 0, loc), "CLASS"),
		occurrence("All day", true,
			time.Date(2026, 10, 21, 0, 0, 0, 0, loc),
			time.Date(2026, 10, 22, 0, 0, 0, 0, loc), ""),
		occurrence("Morning", false,
			time.Date(2026, 10, 21, 8, 0, 0, 0, loc),
			time.Date(2026, 10, 21, 9, 0, 0, 0, loc), ""),
		occurrence("Past", false,
			time.Date(2026, 10, 20, 7, 0, 0, 0, loc),
			time.Date(2026, 10, 20, 8, 0, 0, 0, loc), ""),
		occurrence("Too far", false,
			time.Date(2026, 10, 28, 8, 0, 0, 0, loc),
			time.Date(2026, 10, 28, 9, 0, 0, 0, loc), ""),
	}
	got := weekView(occs, now)
	if strings.Contains(got, "Past") || strings.Contains(got, "Too far") {
		t.Fatalf("weekView included an out-of-window event:\n%s", got)
	}
	if !strings.Contains(got, "Today, Tue 20 Oct") {
		t.Errorf("missing today label:\n%s", got)
	}
	if !strings.Contains(got, "Tomorrow, Wed 21 Oct") {
		t.Errorf("missing tomorrow label:\n%s", got)
	}
	if !strings.Contains(got, "[CLASS]") {
		t.Errorf("missing category:\n%s", got)
	}
	allDay := strings.Index(got, "All day")
	morning := strings.Index(got, "Morning")
	if allDay < 0 || morning < 0 || allDay > morning {
		t.Errorf("all-day should sort first within its day:\n%s", got)
	}
}

func TestWeekViewOvernightLandsToday(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, loc)
	occs := []Occurrence{
		occurrence("Overnight", false,
			time.Date(2026, 10, 19, 23, 0, 0, 0, loc),
			time.Date(2026, 10, 20, 2, 0, 0, 0, loc), ""),
	}
	got := weekView(occs, now)
	if !strings.Contains(got, "Today, Tue 20 Oct") || !strings.Contains(got, "Overnight") {
		t.Fatalf("overnight event should land on today:\n%s", got)
	}
}

func TestWeekViewEmpty(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if got := weekView(nil, now); got != "Nothing in the next 8 days." {
		t.Fatalf("weekView(nil) = %q", got)
	}
}
