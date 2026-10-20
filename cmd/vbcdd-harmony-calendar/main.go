// Command vbcdd-harmony-calendar reads a CalDAV calendar and answers two
// questions the scheduler profile asks: what is left of today, and when the
// next alarm is due.
//
// It is a tool of that profile. Its configuration sits beside it in the
// profile's tools directory, and the daemon tells it where that is through
// `VBCDD_HARMONY_PROFILE_DIR`.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// pollInterval is how long `next` asks to be left alone when it has nothing to
// report, so a calendar that is quiet is not re-read on every tick.
const pollInterval = 10 * time.Minute

// readTimeout bounds one reading of the calendar.
const readTimeout = 2 * time.Minute

// plan is what `next` prints: when the job should run, when to ask again, and
// what to ask.
type plan struct {
	RunAt  string `json:"run_at,omitempty"`
	PollAt string `json:"poll_at"`
	Prompt string `json:"prompt,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vbcdd-harmony-calendar:", err)
		os.Exit(1)
	}
}

// run dispatches one invocation.
func run(args []string) error {
	command := "today"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("vbcdd-harmony-calendar", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to calendar.toml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	switch command {
	case "today":
		return today(cfg)
	case "next":
		return next(cfg)
	case "week":
		return week(cfg)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

// today prints what is running now and what is still to come.
func today(cfg *Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	at := now()
	occs, err := cfg.calendar().Occurrences(ctx, startOfDay(at), startOfDay(at).Add(dayDuration))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, upcoming(occs, at))
	return nil
}

// week prints the events of the current local day and the next seven, one
// section per day.
func week(cfg *Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	at := now()
	from := startOfDay(at).AddDate(0, 0, -1) // A day back, so an overnight event lands on today.
	occs, err := cfg.calendar().Occurrences(ctx, from, startOfDay(at).AddDate(0, 0, weekDays-1))
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, weekView(occs, at))
	return nil
}

// next prints when the next alarm is due, and what to say about it.
//
// The alarm it reports is recorded as delivered, so a restart does not report
// it twice. A daemon that dies between the answer and the run loses that one
// alarm, which is the price of not tracking a delivery handshake the daemon
// cannot acknowledge.
func next(cfg *Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	at := now()
	calendar := cfg.calendar()
	occs, err := calendar.Occurrences(ctx, startOfDay(at), startOfDay(at).AddDate(0, 0, cfg.HorizonDays))
	if err != nil {
		return err
	}
	due := fires(occs, at)

	state, err := openState(cfg.State)
	if err != nil {
		return err
	}
	keys := make(map[string]bool, len(due))
	for _, fire := range due {
		keys[fire.Key] = true
	}
	state.prune(keys)

	answer := plan{PollAt: at.Add(pollInterval).UTC().Format(time.RFC3339)}
	for _, fire := range due {
		if state.delivered(fire.Key) || fire.At.After(at) {
			continue
		}
		state.deliver(fire.Key)
		answer.RunAt = at.UTC().Format(time.RFC3339)
		answer.Prompt = "An alarm is due. Send it to the owner now, as one line, " +
			"with no preamble and no tool narration:\n\n" + formatAlarm(fire)
		break
	}
	if err = state.save(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(answer)
}
