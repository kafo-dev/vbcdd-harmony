// Package hook runs the commands a profile declares before a message reaches
// its agent. An `alter` hook rewrites the prompt and the agent still runs; an
// `override` hook answers in the agent's place.
package hook

import (
	"context"
	"fmt"
	"strings"

	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/profile"
)

// Run is what a hook learns about the turn it is part of, and what it receives
// as JSON on standard input.
type Run struct {
	// Prompt is the message, or the prompt of a timed job.
	Prompt string `json:"prompt"`
	// Reason is why the turn happened: "message" for a message, and
	// "job:<name>" for a timed run.
	Reason string `json:"reason"`
	// Topic is the ntfy topic the message arrived on.
	Topic string `json:"topic"`
	// Profile is the profile answering.
	Profile string `json:"profile"`
	// From is the sender, as ntfy reported it.
	From string `json:"from"`
}

// Result is the outcome of one hook chain.
type Result struct {
	// Prompt is what the agent receives.
	Prompt string
	// Reply is the answer a hook gave, when it overrode the agent.
	Reply string
	// Overridden reports whether a hook answered in the agent's place, which
	// means the agent must not run.
	Overridden bool
}

// Runner applies a profile's hooks.
type Runner struct {
	// Command runs one hook. Its timeout and its environment come from here.
	Command command.Runner
}

// Apply runs every hook whose pattern matches the message, in declaration
// order. An `alter` hook hands the running prompt to its command and takes the
// output as the new one; an `override` hook answers and ends the chain.
//
// A pattern matches the message as it arrived, not the prompt an earlier hook
// rewrote, so one hook cannot widen another's reach.
func (r Runner) Apply(ctx context.Context, hooks []profile.Hook, run Run) (Result, error) {
	result := Result{Prompt: run.Prompt}
	// A hook that fails and says nothing would rewrite the prompt to an empty
	// one, or answer with an empty reply, so a non-zero exit is an error here.
	runner := r.Command
	runner.FailOnError = true
	for _, hook := range hooks {
		if !hook.Matches(run.Prompt) {
			continue
		}
		out, err := runner.Run(ctx, hook.Command, r.arguments(run, result.Prompt))
		if err != nil {
			return result, fmt.Errorf("hook %s: %w", label(hook), err)
		}
		switch hook.Mode {
		case profile.Alter:
			result.Prompt = trim(out)
		case profile.Override:
			result.Reply = trim(out)
			result.Overridden = true
			return result, nil
		}
	}
	return result, nil
}

// arguments builds what one hook receives.
func (r Runner) arguments(run Run, prompt string) map[string]any {
	return map[string]any{
		"prompt":  prompt,
		"reason":  run.Reason,
		"topic":   run.Topic,
		"profile": run.Profile,
		"from":    run.From,
	}
}

// trim drops the trailing newline a shell command leaves behind, which would
// otherwise reach the agent or the reader.
func trim(out string) string {
	return strings.TrimRight(out, "\n")
}

// label identifies a hook in an error, by name when it has one.
func label(hook profile.Hook) string {
	if hook.Name != "" {
		return hook.Name
	}
	return hook.Command
}
