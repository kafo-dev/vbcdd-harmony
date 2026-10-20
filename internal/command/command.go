// Package command runs a profile's external commands: the target behind a
// tool, a hook, or a job. One command receives its arguments as JSON on
// standard input and again as `VBCDD_ARG_<NAME>` variables, and answers on
// standard output.
package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// argEnvPrefix starts the name of the environment variable that carries one
// argument, so a shell command reaches an argument without parsing the JSON.
const argEnvPrefix = "VBCDD_ARG_"

// killGrace is how long [exec.Cmd.Wait] waits for the pipes to close after a
// canceled command is killed.
const killGrace = time.Second

// Runner runs a command as a child process.
type Runner struct {
	// Dir is the working directory of the child. Empty means the parent's.
	Dir string
	// Env is the child's whole environment. Nil inherits the parent's.
	Env []string
	// Shell interprets a target. Empty takes $SHELL, and a POSIX shell when
	// the environment names none.
	Shell string
	// Timeout bounds one call. Zero leaves it unbounded.
	Timeout time.Duration
	// Log receives the diagnostics. Nil drops them.
	Log *slog.Logger
	// FailOnError turns a non-zero exit into an error. A tool leaves it
	// false, because its output is what the model needs to see; a hook sets
	// it, because a hook that fails silently rewrites the prompt to nothing.
	FailOnError bool
}

// Run runs one target and returns what it prints. The target is a command
// line, which the shell interprets, so a tool may be a pipeline or a compound
// command rather than one executable.
//
// Standard output always returns, whatever the command exits with. A non-zero
// exit is logged and yields no error, because the output usually explains the
// failure; a timeout or a command that never starts does yield one.
func (r Runner) Run(ctx context.Context, target string, args map[string]any) (string, error) {
	payload, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("%s: encode arguments: %w", target, err)
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	//nolint:gosec // the target comes from the profile, and runs the shell by design.
	cmd := exec.CommandContext(ctx, r.shell(), "-c", target)
	cmd.Dir = r.Dir
	cmd.Env = r.childEnv(args)
	cmd.Stdin = bytes.NewReader(payload)
	// A wrapper script outlives the command it started unless the whole group
	// dies, and its children would hold the pipes open past the timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = killGrace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return stdout.String(), fmt.Errorf("%s: %w", target, ctx.Err())
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", fmt.Errorf("%s: %w", target, err)
		}
		r.logf(ctx, "%s exited %d: %s", target, exit.ExitCode(), strings.TrimSpace(stderr.String()))
		if r.FailOnError {
			return stdout.String(), fmt.Errorf("%s exited %d", target, exit.ExitCode())
		}
	}
	return stdout.String(), nil
}

// shell returns the interpreter a target runs under.
func (r Runner) shell() string {
	if r.Shell != "" {
		return r.Shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// killGroup kills a command and every process it started. The runner puts the
// command in its own process group, so a negative pid reaches the group.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// childEnv returns the child's environment: the configured one, plus one
// variable per argument.
func (r Runner) childEnv(args map[string]any) []string {
	env := r.Env
	for _, name := range sortedKeys(args) {
		env = append(env, argEnvPrefix+envName(name)+"="+envValue(args[name]))
	}
	return env
}

// envName rewrites an argument name into the upper-case form an environment
// variable name allows.
func envName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(unicode.ToUpper(r))
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// envValue renders an argument as the text of an environment variable.
func envValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// sortedKeys returns a map's keys in a stable order, so the environment does
// not move between runs.
func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// logf writes one diagnostic line, and drops it when the runner has no logger.
func (r Runner) logf(ctx context.Context, format string, args ...any) {
	if r.Log == nil {
		return
	}
	r.Log.InfoContext(ctx, fmt.Sprintf(format, args...))
}
