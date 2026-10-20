package job

import (
	"strings"
	"testing"
	"time"

	"vbcdd.kafo.dev/harmony/internal/profile"
)

func TestParseReadsAPlan(t *testing.T) {
	t.Parallel()
	plan, err := Parse([]byte(`{
		"run_at": "2026-10-20T05:32:00Z",
		"poll_at": "2026-10-20T05:42:00Z",
		"prompt": "An alarm is due.",
		"extra": "ignored"
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	runAt, pollAt, err := plan.Times()
	if err != nil {
		t.Fatalf("Times: %v", err)
	}
	if !runAt.Equal(time.Date(2026, 10, 20, 5, 32, 0, 0, time.UTC)) {
		t.Errorf("run_at = %s", runAt)
	}
	if !pollAt.Equal(time.Date(2026, 10, 20, 5, 42, 0, 0, time.UTC)) {
		t.Errorf("poll_at = %s", pollAt)
	}
	if plan.Prompt != "An alarm is due." || !plan.Overrides() {
		t.Errorf("plan = %+v, want an overriding prompt", plan)
	}
}

func TestParseAcceptsAnEmptyPlan(t *testing.T) {
	t.Parallel()
	plan, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	runAt, pollAt, err := plan.Times()
	if err != nil {
		t.Fatalf("Times: %v", err)
	}
	if !runAt.IsZero() || !pollAt.IsZero() {
		t.Errorf("times = %s %s, want none", runAt, pollAt)
	}
	if plan.Overrides() {
		t.Error("Overrides = true, want the job's own prompt")
	}
}

func TestParseRejectsBadPlans(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"not JSON", "run_at", "not JSON"},
		{"bad run_at", `{"run_at":"tomorrow"}`, "run_at \"tomorrow\""},
		{"bad poll_at", `{"poll_at":"soon"}`, "poll_at \"soon\""},
		{"wrong type", `{"run_at":5}`, "not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.body))
			if err == nil {
				t.Fatal("Parse accepted the plan")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestNextForATimeOfDay(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	schedule := profile.Schedule{Time: "05:32"}
	cases := []struct {
		name  string
		after time.Time
		want  time.Time
	}{
		{
			"earlier the same day",
			time.Date(2026, 10, 20, 4, 0, 0, 0, loc),
			time.Date(2026, 10, 20, 5, 32, 0, 0, loc),
		},
		{
			"after the time, so tomorrow",
			time.Date(2026, 10, 20, 6, 0, 0, 0, loc),
			time.Date(2026, 10, 21, 5, 32, 0, 0, loc),
		},
		{
			"exactly at the time, so tomorrow",
			time.Date(2026, 10, 20, 5, 32, 0, 0, loc),
			time.Date(2026, 10, 21, 5, 32, 0, 0, loc),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Next(schedule, tc.after, loc)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("Next = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNextForAWeekday(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	schedule := profile.Schedule{Time: "07:00", Weekday: "Sun"}
	// 2026-10-20 is a Tuesday.
	cases := []struct {
		name  string
		after time.Time
		want  time.Time
	}{
		{
			"before Sunday",
			time.Date(2026, 10, 20, 8, 0, 0, 0, loc),
			time.Date(2026, 10, 25, 7, 0, 0, 0, loc),
		},
		{
			"on Sunday before the time",
			time.Date(2026, 10, 25, 6, 0, 0, 0, loc),
			time.Date(2026, 10, 25, 7, 0, 0, 0, loc),
		},
		{
			"on Sunday after the time",
			time.Date(2026, 10, 25, 8, 0, 0, 0, loc),
			time.Date(2026, 11, 1, 7, 0, 0, 0, loc),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Next(schedule, tc.after, loc)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("Next = %s, want %s", got, tc.want)
			}
			if got.Weekday() != time.Sunday {
				t.Fatalf("Next = %s, want a Sunday", got)
			}
		})
	}
}

// TestNextForAnIntervalAlignsToTheClock checks that an interval counts from the
// clock rather than from the last run, so a restart does not shift every run
// that follows.
func TestNextForAnIntervalAlignsToTheClock(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	schedule := profile.Schedule{IntervalMinutes: 10}
	after := time.Date(2026, 10, 20, 5, 33, 12, 0, loc)
	got, err := Next(schedule, after, loc)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := time.Date(2026, 10, 20, 5, 40, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("Next = %s, want %s", got, want)
	}
}

func TestNextUsesTheLocation(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Skipf("timezone data unavailable: %v", err)
	}
	schedule := profile.Schedule{Time: "05:32"}
	after := time.Date(2026, 10, 20, 4, 0, 0, 0, loc)
	got, err := Next(schedule, after, loc)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.In(loc).Format(clockLayout) != "05:32" {
		t.Fatalf("Next = %s, want 05:32 local", got.In(loc))
	}
}

func TestNextRejectsABadSchedule(t *testing.T) {
	t.Parallel()
	if _, err := Next(profile.Schedule{Time: "5:32"}, time.Now(), time.UTC); err == nil {
		t.Fatal("Next accepted a time that is not HH:MM")
	}
	if _, err := Next(profile.Schedule{Time: "05:32", Weekday: "Funday"}, time.Now(), time.UTC); err == nil {
		t.Fatal("Next accepted a weekday that is not an abbreviation")
	}
}
