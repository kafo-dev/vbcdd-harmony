package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// command is one owner instruction answered without the agent.
type command struct {
	name string
	help string
	run  func(ctx context.Context, b *Bridge, args []string) (string, error)
}

// commands returns the registry of slash commands, keyed by name.
func commands() map[string]command {
	return map[string]command{
		"clear": {name: "clear", help: "clear the pi chat transcript", run: cmdClear},
		"help":  {name: "help", help: "list the commands", run: cmdHelp},
		"now":   {name: "now", help: "list the events running or still to come today", run: cmdNow},
	}
}

// runCommand answers a slash command. The second result is false when the body
// does not start with a slash and belongs to the agent. A slash command never
// reaches the agent, even when its name is unknown.
func (b *Bridge) runCommand(ctx context.Context, body string) (string, bool, error) {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false, nil
	}
	fields := strings.Fields(trimmed)
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	cmd, ok := commands()[name]
	if !ok {
		return fmt.Sprintf("Unknown command `/%s`. Try `/help`.", name), true, nil
	}
	reply, err := cmd.run(ctx, b, fields[1:])
	return reply, true, err
}

// cmdNow lists the events running now or still to come on the current day.
func cmdNow(ctx context.Context, b *Bridge, _ []string) (string, error) {
	if err := b.refresh(ctx); err != nil {
		return "", err
	}
	now := b.now().In(b.cfg.loc)
	b.mu.Lock()
	occs := append([]Occurrence(nil), b.occs...)
	b.mu.Unlock()
	return upcoming(occs, now), nil
}

// cmdClear drops the saved pi transcript, so the next turn starts an empty
// conversation. Facts, state, and mail stay untouched.
func cmdClear(_ context.Context, b *Bridge, _ []string) (string, error) {
	removed, err := b.agent.ClearTranscript()
	if err != nil {
		return "", err
	}
	if removed == 0 {
		return "The chat transcript is already empty.", nil
	}
	return "Cleared the chat transcript. Facts, state, and mail are untouched.", nil
}

// upcoming selects and formats the occurrences that end after `now` and start
// before local midnight. An all-day event sorts first; the rest sort by start.
func upcoming(occs []Occurrence, now time.Time) string {
	dayEnd := startOfDay(now).Add(dayDuration)
	var todays []Occurrence
	for _, occ := range occs {
		if occ.End.After(now) && occ.Start.Before(dayEnd) {
			todays = append(todays, occ)
		}
	}
	if len(todays) == 0 {
		return "Nothing else today."
	}
	sort.Slice(todays, func(i, j int) bool {
		if todays[i].AllDay != todays[j].AllDay {
			return todays[i].AllDay
		}
		return todays[i].Start.Before(todays[j].Start)
	})

	var sb strings.Builder
	for i, occ := range todays {
		if i > 0 {
			sb.WriteString("\n")
		}
		marker := "•"
		if !occ.Start.After(now) && occ.End.After(now) {
			marker = "▶"
		}
		if occ.AllDay {
			fmt.Fprintf(&sb, "%s all day — %s", marker, occ.Summary)
		} else {
			fmt.Fprintf(&sb, "%s %s–%s — %s", marker, occ.Start.Format("15:04"), occ.End.Format("15:04"), occ.Summary)
		}
		if occ.Category != "" && occ.Category != "-" {
			fmt.Fprintf(&sb, "  [%s]", occ.Category)
		}
	}
	return sb.String()
}

// cmdHelp lists the commands, one per line, sorted by name.
func cmdHelp(_ context.Context, _ *Bridge, _ []string) (string, error) {
	registry := commands()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	for i, name := range names {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "`/%s` — %s", name, registry[name].help)
	}
	return sb.String(), nil
}
