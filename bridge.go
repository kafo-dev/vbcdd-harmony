package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// turnTimeout is the deadline of one agent turn, including the
	// attachment downloads that precede it.
	turnTimeout = 20 * time.Minute

	// tickInterval is how often the bridge looks for a due alarm.
	tickInterval = 20 * time.Second
)

// State survives a restart: the last digest day, the last mail triage day, and
// the keys of the delivered alarms.
type State struct {
	LastDigest     string   `json:"last_digest,omitempty"`
	LastMailTriage string   `json:"last_mail_triage,omitempty"`
	Fired          []string `json:"fired,omitempty"`
}

// Fire is one scheduled notification.
type Fire struct {
	Key    string
	At     time.Time
	Occ    Occurrence
	Offset time.Duration
}

// Bridge wires the calendar, the agent, and ntfy together.
type Bridge struct {
	cfg   *Config
	cal   *Calendar
	agent *Agent
	ntfy  *Ntfy
	log   *slog.Logger
	now   func() time.Time

	mu    sync.Mutex
	state State
	fired map[string]bool
	occs  []Occurrence
	fires []Fire
	inbox chan Incoming
}

// NewBridge constructs the bridge. `now` may be nil for the system clock.
func NewBridge(
	cfg *Config, cal *Calendar, agent *Agent, ntfy *Ntfy, log *slog.Logger, now func() time.Time,
) *Bridge {
	if now == nil {
		now = time.Now
	}
	return &Bridge{
		cfg:   cfg,
		cal:   cal,
		agent: agent,
		ntfy:  ntfy,
		log:   log,
		now:   now,
		fired: map[string]bool{},
		inbox: ntfy.Messages(),
	}
}

// Run serves the bridge until the context is canceled.
func (b *Bridge) Run(ctx context.Context) error {
	if err := b.loadState(); err != nil {
		return err
	}
	go func() {
		if err := b.ntfy.Subscribe(ctx, b.logf); err != nil && ctx.Err() == nil {
			b.logf("subscribe: %v", err)
		}
	}()
	go b.agentLoop(ctx)

	if err := b.refresh(ctx); err != nil {
		b.logf("initial refresh: %v", err)
	}

	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	refresh := time.NewTicker(time.Duration(b.cfg.RefreshMinutes) * time.Minute)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			b.tick(ctx)
		case <-refresh.C:
			if err := b.refresh(ctx); err != nil {
				b.logf("refresh: %v", err)
			}
		}
	}
}

func (b *Bridge) agentLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-b.inbox:
			b.handleBatch(ctx, b.drain(msg))
		}
	}
}

// drain collects the messages already waiting behind the first one, so a burst
// of owner messages runs as one turn instead of one turn each.
func (b *Bridge) drain(first Incoming) []Incoming {
	batch := []Incoming{first}
	for {
		select {
		case next := <-b.inbox:
			batch = append(batch, next)
		default:
			return batch
		}
	}
}

// handleBatch answers the slash commands in the batch and sends the rest to the
// agent as one turn. A batch with only commands skips the agent.
func (b *Bridge) handleBatch(ctx context.Context, batch []Incoming) {
	runCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()

	var replies []string
	prompts := make([]string, 0, len(batch))
	bodies := make([]string, 0, len(batch))
	for _, msg := range batch {
		if reply, handled, err := b.runCommand(runCtx, msg.Body); handled {
			if err != nil {
				reply = fmt.Sprintf("The command failed: %v", err)
			}
			replies = append(replies, reply)
			continue
		}
		prompt := msg.Body
		if msg.Attachment != nil {
			path, err := b.ntfy.Download(runCtx, b.mediaDir(), *msg.Attachment)
			if err != nil {
				b.logf("attachment: %v", err)
				replies = append(replies, fmt.Sprintf("The attachment failed: %v", err))
				continue
			}
			prompt = withAttachment(prompt, path, msg.Attachment.MimeType)
		}
		prompts = append(prompts, prompt)
		bodies = append(bodies, msg.Body)
	}
	if len(replies) > 0 {
		b.send(ctx, strings.Join(replies, "\n\n"))
	}
	if len(prompts) == 0 {
		return
	}
	b.logf("agent input (%d): %s", len(prompts), strings.Join(bodies, " | "))

	reply, err := b.agent.Ask(runCtx, joinBatch(prompts))
	if err != nil {
		b.logf("agent: %v", err)
		b.send(ctx, fmt.Sprintf("The agent failed: %v", err))
		return
	}
	if reply == "" {
		reply = "(no reply)"
	}
	b.send(ctx, reply)
	if err = b.refresh(ctx); err != nil {
		b.logf("post-agent refresh: %v", err)
	}
	b.flushOutbox(runCtx)
}

// joinBatch frames a burst of messages as one prompt, in order.
func joinBatch(prompts []string) string {
	if len(prompts) == 1 {
		return prompts[0]
	}
	parts := make([]string, len(prompts))
	for i, prompt := range prompts {
		parts[i] = fmt.Sprintf("[message %d]\n%s", i+1, prompt)
	}
	return "The following messages arrived in order. Handle them as one turn:\n\n" + strings.Join(parts, "\n\n")
}

// mediaDir holds downloaded attachments inside the state directory, which is
// also the agent's working directory.
func (b *Bridge) mediaDir() string {
	return filepath.Join(b.cfg.StateDir, "media")
}

// outboxDir is where the agent leaves a file for the bridge to publish as an
// ntfy attachment. It sits inside the state directory, which is also the
// agent's working directory.
func (b *Bridge) outboxDir() string {
	return filepath.Join(b.cfg.StateDir, "outbox")
}

// withAttachment points the agent at the downloaded file so `read` or `bash`
// can open it before the answer.
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

// flushOutbox publishes every file the agent left in the outbox as one ntfy
// attachment, then removes it. A file that fails or exceeds the cap is renamed
// with a `.failed` suffix and kept for inspection.
func (b *Bridge) flushOutbox(ctx context.Context) {
	entries, err := os.ReadDir(b.outboxDir())
	if err != nil {
		if !os.IsNotExist(err) {
			b.logf("outbox: %v", err)
		}
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasSuffix(name, ".failed") {
			continue
		}
		path := filepath.Join(b.outboxDir(), name)
		info, statErr := os.Stat(path)
		if statErr != nil {
			b.logf("outbox: %v", statErr)
			continue
		}
		if info.Size() > maxOutboxBytes {
			b.logf("outbox: %s exceeds %d bytes", name, maxOutboxBytes)
			b.failOutbox(name)
			continue
		}
		if err = b.ntfy.SendFile(ctx, path, name, b.cfg.DisplayName); err != nil {
			b.logf("outbox: publish %s: %v", name, err)
			b.failOutbox(name)
			continue
		}
		if err = os.Remove(path); err != nil {
			b.logf("outbox: remove %s: %v", name, err)
		}
	}
}

// failOutbox marks a file the bridge could not publish, so the next turn does
// not retry it.
func (b *Bridge) failOutbox(name string) {
	from := filepath.Join(b.outboxDir(), name)
	if err := os.Rename(from, from+".failed"); err != nil {
		b.logf("outbox: mark %s: %v", name, err)
	}
}

func (b *Bridge) send(ctx context.Context, text string) {
	if err := b.ntfy.Send(ctx, Message{Title: b.cfg.DisplayName, Body: text}); err != nil {
		b.logf("send: %v", err)
	}
}

// enqueue puts a synthetic message on the agent queue, so a scheduled run
// serializes with the owner's messages instead of racing the agent.
func (b *Bridge) enqueue(body string) {
	select {
	case b.inbox <- Incoming{Body: body}:
	default:
		b.logf("agent queue full; dropped a scheduled run")
	}
}

func (b *Bridge) statePath() string {
	return filepath.Join(b.cfg.StateDir, "state.json")
}

func (b *Bridge) loadState() error {
	if err := os.MkdirAll(b.cfg.StateDir, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(b.statePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &b.state); err != nil {
		return err
	}
	for _, key := range b.state.Fired {
		b.fired[key] = true
	}
	return nil
}

func (b *Bridge) saveState() {
	b.mu.Lock()
	b.state.Fired = b.state.Fired[:0]
	for key := range b.fired {
		b.state.Fired = append(b.state.Fired, key)
	}
	data, err := json.Marshal(b.state)
	b.mu.Unlock()
	if err != nil {
		b.logf("save state: %v", err)
		return
	}
	tmp := b.statePath() + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		b.logf("save state: %v", err)
		return
	}
	if err = os.Rename(tmp, b.statePath()); err != nil {
		b.logf("save state: %v", err)
	}
}

// logf writes one diagnostic line. The callers pass a format and its
// arguments, and the line reaches the logger as one message. A bridge that a
// test built without a logger drops the line.
func (b *Bridge) logf(format string, args ...any) {
	if b.log == nil {
		return
	}
	b.log.Info(fmt.Sprintf(format, args...))
}
