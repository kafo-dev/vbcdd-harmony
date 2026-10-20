package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// argEnvPrefix starts the name of the environment variable that carries one
// argument, so a shell target reaches an argument without parsing the JSON.
const argEnvPrefix = "VBCDD_ARG_"

// killGrace is how long [exec.Cmd.Wait] waits for the pipes to close after a
// canceled target is killed.
const killGrace = time.Second

// Runner runs a tool's target as a child process.
type Runner struct {
	// Dir is the working directory of the child. Empty means the parent's.
	Dir string
	// Env is the child's whole environment. Nil inherits the parent's.
	Env []string
	// Timeout bounds one call. Zero leaves it unbounded.
	Timeout time.Duration
	// Log receives the diagnostics. Nil drops them.
	Log *slog.Logger
}

// Run hands the call's arguments to the target and returns what it prints.
//
// Standard output always returns, whatever the target exits with. A non-zero
// exit is logged and yields no error, because the output usually explains the
// failure; a timeout or a target that never starts does yield one.
func (r Runner) Run(ctx context.Context, spec Spec, args map[string]any) (string, error) {
	payload, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("tool %s: encode arguments: %w", spec.Target, err)
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, spec.Target) //nolint:gosec // the target comes from the profile.
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
		return stdout.String(), fmt.Errorf("tool %s: %w", spec.Target, ctx.Err())
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", fmt.Errorf("tool %s: %w", spec.Target, err)
		}
		r.logf(ctx, "tool %s exited %d: %s", spec.Target, exit.ExitCode(), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// killGroup kills the target and every process it started. The runner puts the
// target in its own process group, so a negative pid reaches the group.
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

// logf writes one diagnostic line, and drops it when the runner has no logger.
func (r Runner) logf(ctx context.Context, format string, args ...any) {
	if r.Log == nil {
		return
	}
	r.Log.InfoContext(ctx, fmt.Sprintf(format, args...))
}
