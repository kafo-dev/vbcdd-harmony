// Package job computes when a profile's timed jobs run, and reads what a job's
// `next` command reports about the run ahead.
package job

import (
	"encoding/json"
	"fmt"
	"time"

	"vbcdd.kafo.dev/harmony/internal/profile"
)

// clockLayout is the time of day a schedule and a plan use.
const clockLayout = "15:04"

// Plan is what a `next` command reports on standard output.
type Plan struct {
	// RunAt is when the job runs, in RFC 3339. Empty means nothing is due.
	RunAt string `json:"run_at"`
	// PollAt is when to ask again, in RFC 3339. Empty leaves the cadence to
	// the job's own schedule.
	PollAt string `json:"poll_at"`
	// Prompt replaces the job's own prompt.
	Prompt string `json:"prompt"`
	// Command replaces the job's own command.
	Command string `json:"command"`
}

// Parse reads a plan and checks the instants it names. A command may add
// fields of its own, which Parse ignores.
func Parse(data []byte) (Plan, error) {
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return Plan{}, fmt.Errorf("plan is not JSON: %w", err)
	}
	if _, _, err := plan.Times(); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Times returns the two instants the plan names. A zero time means the plan
// names none.
func (p Plan) Times() (time.Time, time.Time, error) {
	runAt, err := parseInstant("run_at", p.RunAt)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	pollAt, err := parseInstant("poll_at", p.PollAt)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return runAt, pollAt, nil
}

// Overrides reports whether the plan replaces the job's own prompt or command.
func (p Plan) Overrides() bool {
	return p.Prompt != "" || p.Command != ""
}

// parseInstant reads one instant, where an empty string is no instant at all.
func parseInstant(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not an RFC 3339 instant", field, value)
	}
	return at, nil
}

// Next returns the first instant after `after` at which the schedule fires.
//
// An interval aligns to the clock rather than counting from the last run, so a
// restart does not shift every later run, and a time of day names the next
// matching one, in `loc`.
func Next(s profile.Schedule, after time.Time, loc *time.Location) (time.Time, error) {
	if s.IntervalMinutes > 0 {
		interval := time.Duration(s.IntervalMinutes) * time.Minute
		return after.Truncate(interval).Add(interval), nil
	}
	hour, minute, err := profile.Clock(s.Time)
	if err != nil {
		return time.Time{}, err
	}
	local := after.In(loc)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !candidate.After(after) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	if s.Weekday == "" {
		return candidate, nil
	}
	weekday, err := profile.Day(s.Weekday)
	if err != nil {
		return time.Time{}, err
	}
	for candidate.Weekday() != weekday {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate, nil
}
