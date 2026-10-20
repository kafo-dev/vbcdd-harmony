package bridge

import (
	"context"
	"strings"
	"time"

	"vbcdd.kafo.dev/harmony/internal/hook"
	"vbcdd.kafo.dev/harmony/internal/job"
	"vbcdd.kafo.dev/harmony/internal/profile"
)

// reasonMessage is the reason a hook sees for a message from the topic.
const reasonMessage = "message"

// tickInterval is how often the daemon looks for a due job.
const tickInterval = 20 * time.Second

// runJobs acts on the profile's timed jobs until the context ends.
func (s *service) runJobs(ctx context.Context) {
	jobs := make([]profile.Job, 0, len(s.profile.Jobs))
	for _, j := range s.profile.Jobs {
		if j.On() {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) == 0 {
		return
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, j := range jobs {
				if err := s.actOn(ctx, j); err != nil {
					s.logf("job %s: %v", j.Name, err)
				}
			}
		}
	}
}

// actOn fires a job when its time has come. A job with a `next` command polls
// instead, and runs when that command says to.
func (s *service) actOn(ctx context.Context, j profile.Job) error {
	key := s.profile.Name + "/" + j.Name
	stored, ok := s.state.job(key)
	next, err := due(j.Run, stored, ok, s.now(), s.loc)
	if err != nil {
		return err
	}
	if next.changed {
		if err = s.state.setJob(key, next.next); err != nil {
			return err
		}
	}
	if !next.fired {
		return nil
	}
	if j.Next != "" {
		return s.poll(ctx, j, key)
	}
	return s.runJob(ctx, j, j.Prompt, j.Command)
}

// poll asks a job's `next` command when to run, and runs it when the answer
// says the time has come. The answer may also replace the job's prompt or its
// command.
func (s *service) poll(ctx context.Context, j profile.Job, key string) error {
	out, err := s.runner.Run(ctx, j.Next, s.jobArguments(j))
	if err != nil {
		return err
	}
	plan, err := job.Parse([]byte(out))
	if err != nil {
		return err
	}
	runAt, pollAt, err := plan.Times()
	if err != nil {
		return err
	}
	if !runAt.IsZero() && !runAt.After(s.now()) {
		prompt, command := j.Prompt, j.Command
		if plan.Overrides() {
			prompt, command = plan.Prompt, plan.Command
		}
		if err = s.runJob(ctx, j, prompt, command); err != nil {
			return err
		}
	}
	if !pollAt.IsZero() {
		return s.state.setJob(key, pollAt)
	}
	return nil
}

// runJob runs one job. A job with a prompt calls the agent, and a job without
// one sends its command's output straight to the reader.
func (s *service) runJob(ctx context.Context, j profile.Job, prompt, command string) error {
	if strings.TrimSpace(prompt) == "" {
		out, err := s.runner.Run(ctx, command, s.jobArguments(j))
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) == "" {
			return nil
		}
		return s.send(ctx, strings.TrimRight(out, "\n"))
	}
	reply, err := s.turn(ctx, hook.Run{
		Prompt:  prompt,
		Reason:  "job:" + j.Name,
		Topic:   s.topic,
		Profile: s.profile.Name,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) == "" {
		return nil
	}
	return s.send(ctx, reply)
}

// jobArguments is what a job's commands receive, in the same shape a hook
// receives it.
func (s *service) jobArguments(j profile.Job) map[string]any {
	return map[string]any{
		"reason":  "job:" + j.Name,
		"profile": s.profile.Name,
		"topic":   s.topic,
	}
}
