package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpcoming(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, loc)
	day := startOfDay(now)
	occs := []Occurrence{
		{Summary: "Later", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour)},
		{Summary: "Running", Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
		{Summary: "Holiday", AllDay: true, Start: day, End: day.Add(24 * time.Hour)},
		{Summary: "Past", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)},
		{Summary: "Tomorrow", Start: day.Add(25 * time.Hour), End: day.Add(26 * time.Hour)},
	}
	got := upcoming(occs, now)
	if strings.Contains(got, "Past") {
		t.Errorf("past event listed:\n%s", got)
	}
	if strings.Contains(got, "Tomorrow") {
		t.Errorf("tomorrow event leaked into today:\n%s", got)
	}
	if strings.Index(got, "Holiday") > strings.Index(got, "Running") {
		t.Errorf("all-day event not first:\n%s", got)
	}
	if strings.Index(got, "Running") > strings.Index(got, "Later") {
		t.Errorf("events not sorted by start:\n%s", got)
	}
	if !strings.Contains(got, "▶ all day — Holiday") {
		t.Errorf("running all-day marker missing:\n%s", got)
	}
	if !strings.Contains(got, "▶ 08:00–10:00 — Running") {
		t.Errorf("running marker missing:\n%s", got)
	}
	if !strings.Contains(got, "• 11:00–12:00 — Later") {
		t.Errorf("later event not plain:\n%s", got)
	}
}

func TestUpcomingEmpty(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, loc)
	occs := []Occurrence{{Summary: "Past", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}}
	if got := upcoming(occs, now); got != "Nothing else today." {
		t.Errorf("empty result = %q", got)
	}
}

func TestHelpNamesNow(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	reply, handled, err := b.runCommand(context.Background(), "/help")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !strings.Contains(reply, "/now") || !strings.Contains(reply, "/clear") || !strings.Contains(reply, "/help") {
		t.Errorf("help = %q", reply)
	}
}

func TestClearCommand(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{Home: home, Dir: state, Session: "s"}
	dir, err := agent.SessionDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "2026-01-01T00-00-00-000Z_s.jsonl")
	if err = os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Bridge{agent: agent}

	reply, handled, err := b.runCommand(context.Background(), "/clear")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !strings.Contains(reply, "Cleared the chat transcript") {
		t.Errorf("reply = %q", reply)
	}
	if _, statErr := os.Stat(transcript); !os.IsNotExist(statErr) {
		t.Error("the transcript survived the command")
	}

	reply, _, err = b.runCommand(context.Background(), "/clear")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "already empty") {
		t.Errorf("second reply = %q", reply)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	reply, handled, err := b.runCommand(context.Background(), "  /nope ")
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !strings.Contains(reply, "Unknown command") {
		t.Errorf("reply = %q", reply)
	}
}

func TestPlainMessageReachesAgent(t *testing.T) {
	t.Parallel()
	b := &Bridge{}
	if _, handled, _ := b.runCommand(context.Background(), "hello"); handled {
		t.Error("plain message treated as a command")
	}
}
