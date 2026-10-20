package hook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/profile"
)

// script writes an executable shell script into a temporary directory and
// returns its path, which doubles as a command name.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// hook builds a declared hook around one script.
func newHook(t *testing.T, name string, mode profile.Mode, match, body string) profile.Hook {
	t.Helper()
	declared := profile.Hook{Name: name, Command: script(t, body), Mode: mode, Match: match}
	// Load is what compiles the pattern, so the test goes through it too.
	parsed, err := declared.Compile()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return parsed
}

// apply runs the hooks, retrying once on ETXTBSY. Linux refuses to execute a
// file that any process still holds open for writing, and under a loaded
// parallel test run a child forked by another test can inherit the descriptor
// of a script this one has just written.
func apply(t *testing.T, runner Runner, hooks []profile.Hook, run Run) (Result, error) {
	t.Helper()
	result, err := runner.Apply(context.Background(), hooks, run)
	if err != nil && errors.Is(err, syscall.ETXTBSY) {
		return runner.Apply(context.Background(), hooks, run)
	}
	return result, err
}

func TestApplyAltersThePrompt(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{
		newHook(t, "prefix", profile.Alter, "", `printf 'time: %s\n%s\n' "$(date +%Y)" "$VBCDD_ARG_PROMPT"`),
	}
	got, err := apply(t, Runner{}, hooks, Run{Prompt: "list the files"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Overridden {
		t.Error("Overridden = true, want the agent to run")
	}
	if !strings.HasSuffix(got.Prompt, "list the files") {
		t.Fatalf("prompt = %q, want the message still present", got.Prompt)
	}
	if !strings.HasPrefix(got.Prompt, "time: ") {
		t.Fatalf("prompt = %q, want the prefix", got.Prompt)
	}
}

func TestApplyChainsAlterHooks(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{
		newHook(t, "one", profile.Alter, "", `printf 'one(%s)' "$VBCDD_ARG_PROMPT"`),
		newHook(t, "two", profile.Alter, "", `printf 'two(%s)' "$VBCDD_ARG_PROMPT"`),
	}
	got, err := apply(t, Runner{}, hooks, Run{Prompt: "x"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Prompt != "two(one(x))" {
		t.Fatalf("prompt = %q, want the hooks applied in order", got.Prompt)
	}
}

func TestApplyStopsAtAnOverride(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{
		newHook(t, "clear", profile.Override, `^/clear$`, `printf 'cleared'`),
		newHook(t, "never", profile.Alter, "", `printf 'should not run'`),
	}
	got, err := apply(t, Runner{}, hooks, Run{Prompt: "/clear"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !got.Overridden || got.Reply != "cleared" {
		t.Fatalf("result = %+v, want the override's reply", got)
	}
	if got.Prompt != "/clear" {
		t.Fatalf("prompt = %q, want it untouched", got.Prompt)
	}
}

func TestApplySkipsAnUnmatchedHook(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{
		newHook(t, "clear", profile.Override, `^/clear$`, `printf 'cleared'`),
	}
	got, err := apply(t, Runner{}, hooks, Run{Prompt: "say hi"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Overridden {
		t.Fatalf("result = %+v, want the hook skipped", got)
	}
}

// TestApplyMatchesTheOriginalMessage guards the chain: a pattern must not see
// what an earlier hook wrote, or one hook could widen another's reach.
func TestApplyMatchesTheOriginalMessage(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{
		newHook(t, "writer", profile.Alter, "", `printf '/clear'`),
		newHook(t, "clear", profile.Override, `^/clear$`, `printf 'cleared'`),
	}
	got, err := apply(t, Runner{}, hooks, Run{Prompt: "say hi"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Overridden {
		t.Fatal("Overridden = true, want the override hook to miss")
	}
	if got.Prompt != "/clear" {
		t.Fatalf("prompt = %q", got.Prompt)
	}
}

func TestApplyPassesTheReason(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{
		newHook(
			t,
			"reason",
			profile.Alter,
			"",
			`printf '%s %s %s' "$VBCDD_ARG_REASON" "$VBCDD_ARG_TOPIC" "$VBCDD_ARG_FROM"`,
		),
	}
	run := Run{Prompt: "x", Reason: "job:digest", Topic: "vbcdd-general", From: "phone", Profile: "general"}
	got, err := apply(t, Runner{}, hooks, run)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Prompt != "job:digest vbcdd-general phone" {
		t.Fatalf("prompt = %q, want the run's own fields", got.Prompt)
	}
}

func TestApplyReportsAFailingHook(t *testing.T) {
	t.Parallel()
	hooks := []profile.Hook{newHook(t, "broken", profile.Alter, "", "exit 1")}
	_, err := apply(t, Runner{}, hooks, Run{Prompt: "x"})
	if err == nil {
		t.Fatal("Apply accepted a hook that failed")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error = %q, want the hook named", err)
	}
}

func TestApplyWithoutHooks(t *testing.T) {
	t.Parallel()
	runner := Runner{Command: command.Runner{}}
	got, err := apply(t, runner, nil, Run{Prompt: "x"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Overridden || got.Prompt != "x" {
		t.Fatalf("result = %+v, want the message untouched", got)
	}
}
