package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vbcdd.kafo.dev/harmony/internal/agent"
	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/hook"
	"vbcdd.kafo.dev/harmony/internal/job"
	"vbcdd.kafo.dev/harmony/internal/ntfy"
	"vbcdd.kafo.dev/harmony/internal/profile"
)

// service is one profile's runtime: its transport, its conversation, its hooks,
// and its jobs.
type service struct {
	profile profile.Profile
	topic   string
	title   string
	client  *ntfy.Client
	agent   *agent.Agent
	hooks   hook.Runner
	runner  command.Runner
	state   *stateFile
	log     *slog.Logger
	now     func() time.Time
	loc     *time.Location
	// mediaDir holds the attachments this profile downloads.
	mediaDir string

	mu sync.Mutex
	// pending records that a handoff was asked for from outside, which the
	// next turn acts on because the conversation cannot change under a
	// running one.
	pending bool
}

// serve answers the messages that arrive on the profile's topic.
func (s *service) serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-s.client.Messages():
			if err := s.handle(ctx, msg); err != nil {
				s.logf("message %s: %v", msg.ID, err)
				if sendErr := s.send(ctx, fmt.Sprintf("The turn failed: %v", err)); sendErr != nil {
					s.logf("send: %v", sendErr)
				}
			}
		}
	}
}

// handle answers one message.
func (s *service) handle(ctx context.Context, msg ntfy.Incoming) error {
	prompt := msg.Body
	if msg.Attachment != nil {
		path, err := s.client.Download(ctx, s.mediaDir, *msg.Attachment)
		if err != nil {
			s.logf("attachment: %v", err)
			return s.send(ctx, fmt.Sprintf("The attachment failed: %v", err))
		}
		prompt = withAttachment(prompt, path, msg.Attachment.MimeType)
	}
	reply, err := s.turn(ctx, hook.Run{
		Prompt:  prompt,
		Reason:  reasonMessage,
		Topic:   s.topic,
		Profile: s.profile.Name,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) == "" {
		reply = "(no reply)"
	}
	return s.send(ctx, reply)
}

// turn runs one turn: the hooks first, then the handoff if one is due, then the
// agent. A hook that overrides answers in the agent's place.
func (s *service) turn(ctx context.Context, run hook.Run) (string, error) {
	result, err := s.hooks.Apply(ctx, s.profile.Hooks, run)
	if err != nil {
		return "", err
	}
	if result.Overridden {
		return result.Reply, nil
	}
	if err = s.maybeHandoff(ctx); err != nil {
		s.logf("handoff: %v", err)
	}
	return s.agent.Ask(ctx, result.Prompt)
}

// maybeHandoff hands the conversation off when a threshold has been met or an
// outside request asked for one. A failure leaves the conversation in place,
// which is better than starting an empty one.
func (s *service) maybeHandoff(ctx context.Context) error {
	s.mu.Lock()
	pending := s.pending
	s.pending = false
	s.mu.Unlock()

	handoff := s.profile.Handoff
	if handoff == nil {
		return nil
	}
	if !pending {
		tokens := s.agent.Tokens()
		bytes, err := s.agent.Bytes()
		if err != nil {
			return err
		}
		due, err := handoffDue(handoff, tokens, bytes, s)
		if err != nil {
			return err
		}
		if !due {
			return nil
		}
	}
	return s.handoff(ctx)
}

// handoff runs the handoff instruction and starts the conversation the answer
// describes.
func (s *service) handoff(ctx context.Context) error {
	if err := s.agent.Handoff(ctx); err != nil {
		return err
	}
	s.logf("handed the conversation off")
	key := s.profile.Name
	return s.state.setHandoff(key, s.now())
}

// request records an outside request for a handoff, which the next turn acts
// on.
func (s *service) request() {
	s.mu.Lock()
	s.pending = true
	s.mu.Unlock()
}

// clear discards the conversation.
func (s *service) clear() error {
	return s.agent.Reset()
}

// send publishes one message to the profile's topic.
func (s *service) send(ctx context.Context, body string) error {
	return s.client.Send(ctx, ntfy.Message{Title: s.title, Body: body})
}

// logf writes one diagnostic line, and drops it when the service has no logger.
func (s *service) logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Info(fmt.Sprintf(format, args...))
}

// handoffDue reports whether a handoff is due: either counter has reached its
// threshold, or the schedule's next occurrence has passed.
func handoffDue(h *profile.Handoff, tokens int, bytes int64, s *service) (bool, error) {
	if h.Due(tokens, bytes) {
		return true, nil
	}
	if h.After == (profile.Schedule{}) {
		return false, nil
	}
	key := s.profile.Name
	stored, ok := s.state.handoff(key)
	next, err := due(h.After, stored, ok, s.now(), s.loc)
	if err != nil {
		return false, err
	}
	if next.changed {
		if err = s.state.setHandoff(key, next.next); err != nil {
			return false, err
		}
	}
	return next.fired, nil
}

// withAttachment points the agent at the downloaded file, so a tool can open it
// before the answer.
func withAttachment(prompt, path, mimeType string) string {
	note := fmt.Sprintf("The user attached a file saved at %s", path)
	if mimeType != "" {
		note += fmt.Sprintf(" (%s)", mimeType)
	}
	note += ". Open it before answering."
	if strings.TrimSpace(prompt) == "" {
		return note
	}
	return note + "\n\n" + prompt
}

// mediaDirFor returns where a profile keeps the attachments it downloads.
func mediaDirFor(dataDir, name string) string {
	return filepath.Join(dataDir, "profiles", name, "media")
}

// workDir returns where a profile's tools run. It sits apart from the
// conversation and the attachments, so a tool that lists its own directory
// does not see the daemon's bookkeeping.
func workDir(dataDir, name string) string {
	return filepath.Join(dataDir, "profiles", name, "work")
}

// transcriptPath returns where a profile keeps its conversation.
func transcriptPath(dataDir, name string) string {
	return filepath.Join(dataDir, "profiles", name, "conversation.jsonl")
}

// occurrence is what a schedule's next act looks like: whether its stored
// occurrence has passed, the one that follows, and whether that differs from
// what is stored.
type occurrence struct {
	fired   bool
	next    time.Time
	changed bool
}

// due reports whether a schedule's stored occurrence has passed. A schedule
// with no stored occurrence has not fired, and its first occurrence is the next
// one.
func due(schedule profile.Schedule, stored time.Time, ok bool, now time.Time, loc *time.Location) (occurrence, error) {
	if !ok {
		next, err := job.Next(schedule, now, loc)
		if err != nil {
			return occurrence{}, err
		}
		return occurrence{next: next, changed: true}, nil
	}
	if now.Before(stored) {
		return occurrence{next: stored}, nil
	}
	next, err := job.Next(schedule, now, loc)
	if err != nil {
		return occurrence{}, err
	}
	return occurrence{fired: true, next: next, changed: true}, nil
}
