// Package bridge connects the profiles to their topics. It routes a message to
// the profile that answers it, runs that profile's hooks, conversation, and
// timed jobs, and hands the conversation off when it grows too large.
package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"charm.land/fantasy"

	"vbcdd.kafo.dev/harmony/internal/agent"
	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/hook"
	"vbcdd.kafo.dev/harmony/internal/ntfy"
	"vbcdd.kafo.dev/harmony/internal/profile"
	"vbcdd.kafo.dev/harmony/internal/transcript"
)

// Config wires the daemon.
type Config struct {
	// DisplayName titles every message the daemon publishes.
	DisplayName string
	// NtfyURL is the ntfy server.
	NtfyURL string
	// NtfyToken authenticates it.
	NtfyToken string
	// Model answers every profile's requests.
	Model fantasy.LanguageModel
	// ProviderOptions carries the endpoint-specific settings.
	ProviderOptions fantasy.ProviderOptions
	// MaxTurns bounds the tool loop of one request.
	MaxTurns int
	// Topics maps an ntfy topic to the profile that answers it.
	Topics map[string]string
	// Profiles are the profiles to serve.
	Profiles []profile.Profile
	// DataDir holds the conversations, the attachments, and the state.
	DataDir string
	// Runner runs the hooks, the jobs, and the tools.
	Runner command.Runner
	// Loc interprets every schedule.
	Loc *time.Location
	// Now reads the clock, nil for the system clock.
	Now func() time.Time
	// Log receives the diagnostics, nil to drop them.
	Log *slog.Logger
}

// Bridge serves every profile until the context is canceled.
type Bridge struct {
	services []*service
	state    *stateFile
	log      *slog.Logger
}

// New builds one service per profile, resuming each conversation where the last
// run left it.
func New(cfg Config) (*Bridge, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	loc := cfg.Loc
	if loc == nil {
		loc = time.UTC
	}
	state, err := loadState(filepath.Join(cfg.DataDir, stateFileName))
	if err != nil {
		return nil, err
	}
	b := &Bridge{state: state, log: cfg.Log}
	var built *service
	for _, declared := range cfg.Profiles {
		if !declared.On() {
			continue
		}
		built, err = newService(cfg, declared, state, now, loc)
		if err != nil {
			return nil, err
		}
		b.services = append(b.services, built)
	}
	return b, nil
}

// newService builds one profile's runtime.
func newService(
	cfg Config, declared profile.Profile, state *stateFile, now func() time.Time, loc *time.Location,
) (*service, error) {
	topic, err := topicOf(cfg.Topics, declared.Name)
	if err != nil {
		return nil, err
	}
	tools := make([]fantasy.AgentTool, 0, len(declared.Tools))
	var built fantasy.AgentTool
	for _, declaredTool := range declared.Tools {
		built, err = agent.NewTool(declaredTool.Name, declaredTool.Spec, cfg.Runner)
		if err != nil {
			return nil, err
		}
		tools = append(tools, built)
	}
	handoff := ""
	if declared.Handoff != nil {
		handoff = declared.Handoff.Prompt
	}
	conversation, err := agent.New(agent.Config{
		Model:           cfg.Model,
		SystemPrompt:    systemPrompt(declared),
		Tools:           tools,
		ProviderOptions: cfg.ProviderOptions,
		MaxTurns:        cfg.MaxTurns,
		Transcript:      transcript.File{Path: transcriptPath(cfg.DataDir, declared.Name)},
		Handoff:         handoff,
	})
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", declared.Name, err)
	}
	return &service{
		profile:  declared,
		topic:    topic,
		title:    cfg.DisplayName,
		client:   ntfy.New(ntfy.Config{BaseURL: cfg.NtfyURL, Topic: topic, Token: cfg.NtfyToken}),
		agent:    conversation,
		hooks:    hook.Runner{Command: cfg.Runner},
		runner:   cfg.Runner,
		state:    state,
		log:      cfg.Log,
		now:      now,
		loc:      loc,
		mediaDir: mediaDirFor(cfg.DataDir, declared.Name),
	}, nil
}

// Run serves every profile until the context is canceled.
func (b *Bridge) Run(ctx context.Context) error {
	for _, service := range b.services {
		go func() {
			if err := service.client.Subscribe(ctx, b.logf); err != nil && ctx.Err() == nil {
				b.logf("subscribe %s: %v", service.profile.Name, err)
			}
		}()
		go service.serve(ctx)
		go service.runJobs(ctx)
	}
	<-ctx.Done()
	return nil
}

// Handoff asks a profile to hand its conversation off at the next turn.
func (b *Bridge) Handoff(name string) error {
	service, err := b.service(name)
	if err != nil {
		return err
	}
	if service.profile.Handoff == nil {
		return fmt.Errorf("profile %s declares no handoff", name)
	}
	service.request()
	return nil
}

// Clear discards a profile's conversation.
func (b *Bridge) Clear(name string) error {
	service, err := b.service(name)
	if err != nil {
		return err
	}
	return service.clear()
}

// service returns the service serving a profile.
func (b *Bridge) service(name string) (*service, error) {
	for _, service := range b.services {
		if service.profile.Name == name {
			return service, nil
		}
	}
	return nil, fmt.Errorf("no profile named %q is served", name)
}

// logf writes one diagnostic line, and drops it when the bridge has no logger.
func (b *Bridge) logf(format string, args ...any) {
	if b.log == nil {
		return
	}
	b.log.Info(fmt.Sprintf(format, args...))
}

// topicOf returns the topic that routes to a profile.
func topicOf(topics map[string]string, name string) (string, error) {
	for topic, routed := range topics {
		if routed == name {
			return topic, nil
		}
	}
	return "", fmt.Errorf("no topic routes to the profile %q", name)
}

// systemPrompt builds the standing instruction from the profile's prompt and
// the documentation beside it.
func systemPrompt(declared profile.Profile) string {
	var builder strings.Builder
	builder.WriteString(declared.SystemPrompt)
	for _, doc := range declared.Docs {
		builder.WriteString("\n\n")
		builder.WriteString(doc.Body)
	}
	return builder.String()
}
