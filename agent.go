package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Agent runs one shell-command agent turn and returns its final message.
type Agent struct {
	Bin        string
	Provider   string
	Model      string
	Session    string
	Tools      []string
	Dir        string
	Home       string
	Sandbox    string
	Loc        *time.Location
	Now        func() time.Time
	AppendArgs []string
}

//go:embed prompt/assistant.md
var assistantPrompt string

//go:embed prompt/mail.md
var mailPrompt string

//go:embed prompt/memory.md
var memoryPrompt string

//go:embed prompt/calendar.md
var calendarPrompt string

// Prompt is the standing instruction appended to every agent run.
var Prompt = strings.Join([]string{
	strings.TrimSpace(assistantPrompt),
	strings.TrimSpace(calendarPrompt),
	strings.TrimSpace(mailPrompt),
	strings.TrimSpace(memoryPrompt),
}, "\n\n")

// timeContext states the current local time in the standing prompt, so a
// long-lived session does not run `date` before every answer.
func (a *Agent) timeContext() string {
	loc := a.Loc
	if loc == nil {
		loc = time.Local
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	return fmt.Sprintf("The current local time is %s (%s), %s UTC.",
		now.In(loc).Format("2006-01-02 15:04"),
		loc.String(),
		now.UTC().Format("15:04"))
}

// turnArgs builds the command line for one turn. A configured sandbox runs
// first and receives the pi binary and its arguments.
func (a *Agent) turnArgs(message string) (string, []string) {
	args := []string{
		"-p",
		"--provider", a.Provider,
		"--model", a.Model,
		"--session-id", a.Session,
		"--tools", strings.Join(a.Tools, ","),
		"--append-system-prompt", Prompt + "\n\n" + a.timeContext(),
	}
	args = append(args, a.AppendArgs...)
	args = append(args, message)
	if a.Sandbox == "" {
		return a.Bin, args
	}
	return a.Sandbox, append([]string{a.Bin}, args...)
}

// Ask runs one turn in the persistent session and returns stdout.
func (a *Agent) Ask(ctx context.Context, message string) (string, error) {
	bin, args := a.turnArgs(message)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = a.Dir
	if a.Home != "" {
		cmd.Env = append(cmd.Environ(), "HOME="+a.Home)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pi: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// SessionDir returns the directory where pi keeps the transcripts for the
// agent's working directory. pi stores a session as
// ~/.pi/agent/sessions/--<cwd>--/<timestamp>_<session-id>.jsonl: it drops the
// leading separator and replaces every separator with a dash.
func (a *Agent) SessionDir() (string, error) {
	home := a.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	dir := a.Dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	slug := strings.TrimPrefix(filepath.Clean(dir), string(filepath.Separator))
	slug = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(slug)
	return filepath.Join(home, ".pi", "agent", "sessions", "--"+slug+"--"), nil
}

// ClearTranscript removes every saved transcript for the session and returns
// the count. The facts repository, the outbox, and the bridge state sit
// outside the session directory, so each one survives.
func (a *Agent) ClearTranscript() (int, error) {
	dir, err := a.SessionDir()
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	suffix := "_" + a.Session + ".jsonl"
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
