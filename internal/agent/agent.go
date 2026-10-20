// Package agent drives one profile's long-lived conversation through the Charm
// Fantasy SDK.
//
// Fantasy owns the loop, the provider translation, the streaming, and the
// retries. This package owns the conversation: what is stored, what the
// endpoint last reported for it, and the handoff that starts a fresh one.
package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"

	"vbcdd.kafo.dev/harmony/internal/transcript"
)

// charsPerToken is the rule of thumb for an endpoint that reports no usage.
const charsPerToken = 4

// Config wires one profile's conversation.
type Config struct {
	// Model answers every request.
	Model fantasy.LanguageModel
	// SystemPrompt stands in front of the conversation.
	SystemPrompt string
	// Tools are the tools the model may call.
	Tools []fantasy.AgentTool
	// ProviderOptions carries the endpoint-specific settings, such as the
	// reasoning effort. A nil value sends none.
	ProviderOptions fantasy.ProviderOptions
	// MaxTurns bounds the tool loop of one request.
	MaxTurns int
	// Transcript is where the conversation is stored and read back.
	Transcript transcript.File
	// Handoff is the instruction that asks the outgoing conversation to write
	// down what the next one needs. Empty leaves the profile without one.
	Handoff string
	// OnText receives the answer as the model streams it.
	OnText func(string)
	// OnReasoning receives the reasoning as the model streams it.
	OnReasoning func(string)
}

// Agent holds one conversation and the engine that advances it.
type Agent struct {
	engine      fantasy.Agent
	transcript  transcript.File
	handoff     string
	onText      func(string)
	onReasoning func(string)
	maxTurns    int
	history     []fantasy.Message
	// reported is the prompt size the endpoint reported for the last request,
	// or zero when it reported none.
	reported int
}

// New resumes the conversation the transcript holds, or starts an empty one.
func New(cfg Config) (*Agent, error) {
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 1
	}
	history, err := cfg.Transcript.Load()
	if err != nil {
		return nil, err
	}
	a := &Agent{
		transcript:  cfg.Transcript,
		handoff:     cfg.Handoff,
		onText:      cfg.OnText,
		onReasoning: cfg.OnReasoning,
		maxTurns:    maxTurns,
		history:     history,
	}
	a.engine = fantasy.NewAgent(
		cfg.Model,
		fantasy.WithSystemPrompt(cfg.SystemPrompt),
		fantasy.WithTools(cfg.Tools...),
		fantasy.WithProviderOptions(cfg.ProviderOptions),
	)
	return a, nil
}

// Ask sends a prompt and returns the assistant's final text.
func (a *Agent) Ask(ctx context.Context, prompt string) (string, error) {
	return a.run(ctx, prompt)
}

// Handoff asks the outgoing conversation to write down what the next one needs,
// then starts that next conversation from the answer. The instruction itself
// stays out of the new conversation; only the answer enters it.
func (a *Agent) Handoff(ctx context.Context) error {
	if a.handoff == "" {
		return fmt.Errorf("the profile declares no handoff prompt")
	}
	summary, err := a.run(ctx, a.handoff)
	if err != nil {
		return err
	}
	if summary == "" {
		return fmt.Errorf("the handoff produced no summary")
	}
	if err = a.Reset(); err != nil {
		return err
	}
	seed := fantasy.NewUserMessage(summary)
	a.history = []fantasy.Message{seed}
	if err = a.transcript.Append(seed); err != nil {
		return err
	}
	return nil
}

// Reset discards the conversation and starts an empty one.
func (a *Agent) Reset() error {
	a.history = nil
	a.reported = 0
	return a.transcript.Reset()
}

// History returns the conversation that is not yet on disk.
func (a *Agent) History() []fantasy.Message {
	return append([]fantasy.Message(nil), a.history...)
}

// Tokens reports the size of the context the endpoint last answered, in
// tokens, and falls back to the characters-per-token rule when the endpoint
// reported none.
func (a *Agent) Tokens() int {
	if a.reported > 0 {
		return a.reported
	}
	return estimateTokens(a.history)
}

// Bytes reports the size of the stored conversation, in bytes.
func (a *Agent) Bytes() (int64, error) {
	return a.transcript.Size()
}

// run advances the conversation by one turn and stores it.
func (a *Agent) run(ctx context.Context, prompt string) (string, error) {
	result, err := a.engine.Stream(ctx, fantasy.AgentStreamCall{
		Prompt:           prompt,
		Messages:         a.history,
		StopWhen:         []fantasy.StopCondition{fantasy.StepCountIs(a.maxTurns)},
		OnTextDelta:      delta(a.onText),
		OnReasoningDelta: delta(a.onReasoning),
	})
	if err != nil {
		return "", fmt.Errorf("agent: %w", err)
	}
	added := make([]fantasy.Message, 0, len(result.Steps)+1)
	added = append(added, fantasy.NewUserMessage(prompt))
	for _, step := range result.Steps {
		added = append(added, step.Messages...)
	}
	a.history = append(a.history, added...)
	a.reported = promptTokens(result.Steps, a.reported)
	if err = a.transcript.Append(added...); err != nil {
		return "", err
	}
	return result.Response.Content.Text(), nil
}

// delta adapts a callback to the SDK's, which also passes the part's kind.
func delta(callback func(string)) func(string, string) error {
	if callback == nil {
		return nil
	}
	return func(_, text string) error {
		callback(text)
		return nil
	}
}

// promptTokens returns the prompt size the last step reported, keeping the
// previous count when the endpoint reported nothing.
func promptTokens(steps []fantasy.StepResult, fallback int) int {
	if len(steps) == 0 {
		return fallback
	}
	if tokens := steps[len(steps)-1].Usage.InputTokens; tokens > 0 {
		return int(tokens)
	}
	return fallback
}

// estimateTokens applies the rule of thumb of one token per `charsPerToken`
// characters.
func estimateTokens(messages []fantasy.Message) int {
	chars := 0
	for _, message := range messages {
		chars += messageChars(message)
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

// messageChars counts the characters of a message that the endpoint bills for.
// Binary and reference parts add nothing.
func messageChars(message fantasy.Message) int {
	chars := 0
	for _, part := range message.Content {
		switch part.GetType() {
		case fantasy.ContentTypeText:
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				chars += len(text.Text)
			}
		case fantasy.ContentTypeReasoning:
			if reasoning, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](part); ok {
				chars += len(reasoning.Text)
			}
		case fantasy.ContentTypeToolCall:
			if call, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
				chars += len(call.ToolName) + len(call.Input)
			}
		case fantasy.ContentTypeToolResult:
			if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				chars += resultChars(result.Output)
			}
		case fantasy.ContentTypeFile, fantasy.ContentTypeSource:
		}
	}
	return chars
}

// resultChars counts the characters a tool result contributes.
func resultChars(output fantasy.ToolResultOutputContent) int {
	if output == nil {
		return 0
	}
	text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](output)
	if !ok {
		return 0
	}
	return len(text.Text)
}
