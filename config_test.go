package main

import (
	"os"
	"path/filepath"
	"testing"
)

const minimalConfig = `
calendula_config = "/home/vbcdd/.config/calendula/config.toml"
expand_script = "/usr/local/bin/expand-recurrences"
state_dir = "/home/vbcdd/.local/share/vbcdd/harmony"
ntfy_url = "https://ntfy.example.com"
ntfy_topic = "vbcdd-harmony"
ntfy_token = "tok"
calendars = ["calendar"]
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigMinimal(t *testing.T) {
	t.Parallel()
	cfg, err := LoadConfig(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NtfyURL != "https://ntfy.example.com" {
		t.Errorf("ntfy_url = %q", cfg.NtfyURL)
	}
	if cfg.NtfyTopic != "vbcdd-harmony" || cfg.NtfyToken != "tok" {
		t.Errorf("ntfy = %q %q", cfg.NtfyTopic, cfg.NtfyToken)
	}
	if cfg.PiSession != "vbcdd-harmony" {
		t.Errorf("pi_session = %q", cfg.PiSession)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("timezone = %q, want the UTC default", cfg.Timezone)
	}
	if cfg.loc == nil {
		t.Error("location not resolved")
	}
}

func TestLoadConfigDefaultsTheStateDir(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	body := `
calendula_config = "/c"
expand_script = "/e"
ntfy_url = "https://ntfy.example.com"
ntfy_topic = "vbcdd-harmony"
ntfy_token = "tok"
calendars = ["calendar"]
`
	cfg, err := LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := filepath.Join(data, "vbcdd", "harmony")
	if cfg.StateDir != want {
		t.Errorf("state_dir = %q, want %q", cfg.StateDir, want)
	}
}

func TestDefaultConfigPathUsesTheXDGDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	want := filepath.Join(root, "vbcdd", "harmony", "config.toml")
	if got := defaultConfigPath(); got != want {
		t.Errorf("defaultConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadConfigMissingTopic(t *testing.T) {
	t.Parallel()
	body := `
calendula_config = "/c"
expand_script = "/e"
state_dir = "/s"
ntfy_url = "https://ntfy.example.com"
ntfy_token = "tok"
calendars = ["calendar"]
`
	if _, err := LoadConfig(writeConfig(t, body)); err == nil {
		t.Fatal("missing ntfy_topic accepted")
	}
}

func TestLoadConfigBadTopic(t *testing.T) {
	t.Parallel()
	body := `
calendula_config = "/c"
expand_script = "/e"
state_dir = "/s"
ntfy_url = "https://ntfy.example.com"
ntfy_topic = "bad topic"
ntfy_token = "tok"
calendars = ["calendar"]
`
	if _, err := LoadConfig(writeConfig(t, body)); err == nil {
		t.Fatal("invalid ntfy_topic accepted")
	}
}
