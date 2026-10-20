// Package provider builds the language model the daemon calls, from the
// OpenAI-compatible endpoint the configuration names.
package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"

	"vbcdd.kafo.dev/harmony/internal/config"
)

// NewModel builds the model every profile calls.
func NewModel(ctx context.Context, cfg config.Config) (fantasy.LanguageModel, error) {
	key, err := apiKey(ctx, cfg)
	if err != nil {
		return nil, err
	}
	provider, err := openaicompat.New(
		openaicompat.WithBaseURL(cfg.BaseURL),
		openaicompat.WithAPIKey(key),
		openaicompat.WithHeaders(cfg.Headers),
	)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	model, err := provider.LanguageModel(ctx, cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("model %s: %w", cfg.Model, err)
	}
	return model, nil
}

// Options translates the configured reasoning effort into the endpoint's own
// options. An unset effort leaves the choice to the endpoint.
func Options(cfg config.Config) fantasy.ProviderOptions {
	effort := strings.TrimSpace(cfg.ReasoningEffort)
	if effort == "" {
		return nil
	}
	return openaicompat.NewProviderOptions(&openaicompat.ProviderOptions{
		ReasoningEffort: new(openai.ReasoningEffort(effort)),
	})
}

// apiKey returns the key the endpoint authenticates with. A configured command
// runs first and wins over the literal key, which suits a secret manager.
func apiKey(ctx context.Context, cfg config.Config) (string, error) {
	if cfg.APIKeyCommand == "" {
		return cfg.APIKey, nil
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	//nolint:gosec // the command comes from the daemon's own configuration.
	cmd := exec.CommandContext(ctx, shell, "-c", cfg.APIKeyCommand)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("api_key_command: %w", err)
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", fmt.Errorf("api_key_command printed nothing")
	}
	return key, nil
}
