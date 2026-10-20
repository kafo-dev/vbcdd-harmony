package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script writes an executable shell script into a temporary directory and
// returns its path, which doubles as a command name.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// run runs a command, retrying once on ETXTBSY. Linux refuses to execute a file
// that any process still holds open for writing, and under a loaded parallel
// test run a child forked by another test can inherit the descriptor of a
// script this one has just written.
func run(t *testing.T, runner Runner, name string, args map[string]any) (string, error) {
	t.Helper()
	out, err := runner.Run(context.Background(), name, args)
	if err != nil && errors.Is(err, syscall.ETXTBSY) {
		return runner.Run(context.Background(), name, args)
	}
	return out, err
}

func TestRunFeedsTheArgumentsOnStdin(t *testing.T) {
	t.Parallel()
	got, err := run(t, Runner{}, script(t, "cat"), map[string]any{"cmd": "ls"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(got) != `{"cmd":"ls"}` {
		t.Fatalf("stdout = %q, want the arguments", got)
	}
}

func TestRunExportsTheArguments(t *testing.T) {
	t.Parallel()
	name := script(t, `printf '%s|%s' "$VBCDD_ARG_CMD" "$VBCDD_ARG_DRY_RUN"`)
	got, err := run(t, Runner{}, name, map[string]any{"cmd": "ls", "dry-run": true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "ls|true" {
		t.Fatalf("stdout = %q, want %q", got, "ls|true")
	}
}

func TestRunUsesTheConfiguredDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := run(t, Runner{Dir: dir}, script(t, "pwd"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(got) != dir {
		t.Fatalf("stdout = %q, want %q", strings.TrimSpace(got), dir)
	}
}

func TestRunReturnsTheOutputOfAFailingCommand(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	runner := Runner{Log: slog.New(slog.NewTextHandler(&logged, nil))}
	got, err := run(t, runner, script(t, "echo out; exit 3"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(got) != "out" {
		t.Fatalf("stdout = %q, want the output", got)
	}
	if !strings.Contains(logged.String(), "exited 3") {
		t.Fatalf("log = %q, want the exit status", logged.String())
	}
}

func TestRunReportsATimeout(t *testing.T) {
	t.Parallel()
	runner := Runner{Timeout: 50 * time.Millisecond}
	if _, err := run(t, runner, script(t, "sleep 5"), nil); err == nil {
		t.Fatal("Run accepted a command that never finished")
	}
}

// TestRunKillsTheWholeGroup guards the timeout: a wrapper script leaves its
// children behind, and those children hold the pipes open, so the call would
// outlive its deadline and hang the caller.
func TestRunKillsTheWholeGroup(t *testing.T) {
	t.Parallel()
	runner := Runner{Timeout: 50 * time.Millisecond}
	start := time.Now()
	if _, err := run(t, runner, script(t, "sleep 30 & wait"), nil); err == nil {
		t.Fatal("Run accepted a command that never finished")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run took %s, want the group killed at the deadline", elapsed)
	}
}

func TestRunReportsAMissingCommand(t *testing.T) {
	t.Parallel()
	if _, err := (Runner{}).Run(context.Background(), "no-such-command-anywhere", nil); err == nil {
		t.Fatal("Run accepted a command that does not exist")
	}
}

func TestEnvName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"cmd":     "CMD",
		"dry-run": "DRY_RUN",
		"maxSize": "MAXSIZE",
		"a.b":     "A_B",
	}
	for name, want := range cases {
		if got := envName(name); got != want {
			t.Errorf("envName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEnvValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value any
		want  string
	}{
		{"ls", "ls"},
		{true, "true"},
		{false, "false"},
		{json.Number("5"), "5"},
		{nil, ""},
		{[]string{"a"}, fmt.Sprint([]string{"a"})},
	}
	for _, tc := range cases {
		got := envValue(tc.value)
		if got != tc.want {
			t.Errorf("envValue(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
