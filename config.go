package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// dirName is the directory the program owns below each XDG base
	// directory, from the configuration root to the state root.
	dirName    = "vbcdd/harmony"
	configName = "config.toml"
)

// Defaults for the settings a configuration file may omit.
const (
	defaultHorizonDays    = 70
	defaultRefreshMinutes = 10
	defaultMailTriageDays = 7
)

// Config is the bridge configuration, read from one TOML file.
type Config struct {
	DisplayName       string   `toml:"display_name"`
	Timezone          string   `toml:"timezone"`
	DigestTime        string   `toml:"digest_time"`
	Calendars         []string `toml:"calendars"`
	CalendulaBin      string   `toml:"calendula_bin"`
	CalendulaConfig   string   `toml:"calendula_config"`
	ExpandScript      string   `toml:"expand_script"`
	PiBin             string   `toml:"pi_bin"`
	PiProvider        string   `toml:"pi_provider"`
	PiModel           string   `toml:"pi_model"`
	PiSession         string   `toml:"pi_session"`
	PiTools           []string `toml:"pi_tools"`
	PiSandbox         string   `toml:"pi_sandbox"`
	StateDir          string   `toml:"state_dir"`
	NtfyURL           string   `toml:"ntfy_url"`
	NtfyTopic         string   `toml:"ntfy_topic"`
	NtfyToken         string   `toml:"ntfy_token"`
	HorizonDays       int      `toml:"horizon_days"`
	RefreshMinutes    int      `toml:"refresh_minutes"`
	MailTriageTime    string   `toml:"mail_triage_time"`
	MailTriageWeekday string   `toml:"mail_triage_weekday"`
	MailTriageDays    int      `toml:"mail_triage_days"`
	Debug             bool     `toml:"debug"`

	loc               *time.Location
	digestHour        int
	digestMin         int
	mailTriageHour    int
	mailTriageMin     int
	mailTriageWeekday int
}

// topicRe is the character set ntfy accepts in a topic name.
var topicRe = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// homeDir returns the user's home directory, or "" when the environment
// names none.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// defaultConfigPath returns $XDG_CONFIG_HOME/vbcdd/harmony/config.toml.
func defaultConfigPath() string {
	root, err := os.UserConfigDir()
	if err != nil {
		root = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(root, dirName, configName)
}

// defaultStateDir returns $XDG_DATA_HOME/vbcdd/harmony, the default home of
// state.json, media/, facts/, and the outbox.
func defaultStateDir() string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, dirName)
	}
	return filepath.Join(homeDir(), ".local", "share", dirName)
}

// LoadConfig reads and validates the configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Timezone:          "UTC",
		DigestTime:        "05:32",
		CalendulaBin:      "calendula",
		PiBin:             "/opt/pi-coding-agent/pi",
		PiProvider:        "openai.compatible.example.com",
		PiModel:           "example-model-name",
		PiSession:         "vbcdd-harmony",
		PiTools:           []string{"bash", "read", "write"},
		HorizonDays:       defaultHorizonDays,
		RefreshMinutes:    defaultRefreshMinutes,
		MailTriageTime:    "07:00",
		MailTriageWeekday: "Sun",
		MailTriageDays:    defaultMailTriageDays,
	}
	if err = toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.StateDir == "" {
		cfg.StateDir = defaultStateDir()
	}

	for field, value := range map[string]string{
		"calendula_config": cfg.CalendulaConfig,
		"expand_script":    cfg.ExpandScript,
		"ntfy_url":         cfg.NtfyURL,
		"ntfy_topic":       cfg.NtfyTopic,
		"ntfy_token":       cfg.NtfyToken,
	} {
		if value == "" {
			return nil, fmt.Errorf("%s: missing %q", path, field)
		}
	}
	if !topicRe.MatchString(cfg.NtfyTopic) {
		return nil, fmt.Errorf("%s: ntfy_topic must match %s", path, topicRe)
	}
	if len(cfg.Calendars) == 0 {
		return nil, fmt.Errorf("%s: missing \"calendars\"", path)
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("%s: timezone: %w", path, err)
	}
	cfg.loc = loc

	digest, err := time.ParseInLocation("15:04", cfg.DigestTime, loc)
	if err != nil {
		return nil, fmt.Errorf("%s: digest_time: %w", path, err)
	}
	cfg.digestHour, cfg.digestMin = digest.Hour(), digest.Minute()

	mailTriage, err := time.ParseInLocation("15:04", cfg.MailTriageTime, loc)
	if err != nil {
		return nil, fmt.Errorf("%s: mail_triage_time: %w", path, err)
	}
	cfg.mailTriageHour, cfg.mailTriageMin = mailTriage.Hour(), mailTriage.Minute()
	cfg.mailTriageWeekday = -1
	if cfg.MailTriageWeekday != "" {
		weekday, weekErr := parseWeekday(cfg.MailTriageWeekday)
		if weekErr != nil {
			return nil, fmt.Errorf("%s: mail_triage_weekday: %w", path, weekErr)
		}
		cfg.mailTriageWeekday = int(weekday)
	}
	if cfg.MailTriageDays <= 0 {
		cfg.MailTriageDays = defaultMailTriageDays
	}
	return cfg, nil
}

// parseWeekday reads a weekday name, either three letters or the full English name.
func parseWeekday(name string) (time.Weekday, error) {
	days := map[string]time.Weekday{
		"sun": time.Sunday, "sunday": time.Sunday,
		"mon": time.Monday, "monday": time.Monday,
		"tue": time.Tuesday, "tuesday": time.Tuesday,
		"wed": time.Wednesday, "wednesday": time.Wednesday,
		"thu": time.Thursday, "thursday": time.Thursday,
		"fri": time.Friday, "friday": time.Friday,
		"sat": time.Saturday, "saturday": time.Saturday,
	}
	day, ok := days[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return 0, fmt.Errorf("unknown weekday %q", name)
	}
	return day, nil
}
