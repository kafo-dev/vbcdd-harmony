// Package config reads the daemon configuration: the ntfy endpoint it
// publishes through, and the map from an ntfy topic to the profile that
// answers that topic.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// defaultDisplayName titles a message when the file names no other.
const defaultDisplayName = "vbcdd-harmony"

// defaultMaxTurns bounds the tool loop when the file names no other bound.
const defaultMaxTurns = 20

// Config is the daemon configuration, read from one TOML file.
type Config struct {
	DisplayName string `toml:"display_name"`
	NtfyURL     string `toml:"ntfy_url"`
	NtfyToken   string `toml:"ntfy_token"`

	// BaseURL is the OpenAI-compatible endpoint every profile calls.
	BaseURL string `toml:"base_url"`
	// APIKey authenticates the endpoint.
	APIKey string `toml:"api_key"`
	// APIKeyCommand prints the key, and wins over APIKey.
	APIKeyCommand string `toml:"api_key_command"`
	// Model names the model every profile calls.
	Model string `toml:"model"`
	// ReasoningEffort is how much the model may spend on reasoning before it
	// answers, as the endpoint defines it. Empty leaves the choice to the
	// endpoint.
	ReasoningEffort string `toml:"reasoning_effort"`
	// MaxTurns bounds the tool loop of one request.
	MaxTurns int `toml:"max_turns"`
	// Headers join every request.
	Headers map[string]string `toml:"headers"`

	// Topics maps an ntfy topic to the profile that answers it.
	Topics map[string]string `toml:"topics"`
}

// topicRe is the character set ntfy accepts in a topic name.
var topicRe = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// profileRe is the character set a profile directory name may use.
var profileRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Load reads and validates the configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse reads the configuration from TOML bytes and checks what the file
// alone determines.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{DisplayName: defaultDisplayName, MaxTurns: defaultMaxTurns}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if err := cfg.check(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Profile returns the profile that answers a topic.
func (c *Config) Profile(topic string) (string, bool) {
	name, ok := c.Topics[topic]
	return name, ok
}

// Validate checks the configuration against the profiles found on disk: every
// topic must name one of them, and every one of them must have a topic.
func (c *Config) Validate(profiles []string) error {
	known := make(map[string]bool, len(profiles))
	for _, name := range profiles {
		known[name] = true
	}
	mapped := make(map[string]bool, len(c.Topics))
	for _, topic := range sortedKeys(c.Topics) {
		name := c.Topics[topic]
		if !known[name] {
			return fmt.Errorf("topic %q names the profile %q, which is not installed", topic, name)
		}
		mapped[name] = true
	}
	var missing []string
	for _, name := range profiles {
		if !mapped[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("no topic routes to the profile(s) %s", strings.Join(missing, ", "))
	}
	return nil
}

// check rejects a configuration the file itself proves wrong.
func (c *Config) check() error {
	for _, required := range []struct {
		name  string
		value string
	}{
		{"ntfy_url", c.NtfyURL},
		{"base_url", c.BaseURL},
		{"model", c.Model},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("%s is empty", required.name)
		}
	}
	if c.APIKey == "" && c.APIKeyCommand == "" {
		return fmt.Errorf("neither api_key nor api_key_command is set")
	}
	if c.MaxTurns < 1 {
		return fmt.Errorf("max_turns is %d, want at least 1", c.MaxTurns)
	}
	for _, topic := range sortedKeys(c.Topics) {
		if !topicRe.MatchString(topic) {
			return fmt.Errorf("topic %q is not a valid ntfy topic name", topic)
		}
		if name := c.Topics[topic]; !profileRe.MatchString(name) {
			return fmt.Errorf("topic %q names the profile %q, which is not a valid profile name", topic, name)
		}
	}
	return nil
}

// sortedKeys returns the keys of a topic map in a stable order, so a message
// about a bad entry does not move between runs.
func sortedKeys(topics map[string]string) []string {
	keys := make([]string, 0, len(topics))
	for key := range topics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
