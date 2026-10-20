package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// The file names and defaults a calendar configuration falls back to.
const (
	// configName is the file the tool reads, in the profile's own directory.
	// It cannot sit in the tools directory, where every `.toml` is a tool
	// declaration.
	configName = "calendar.toml"

	// defaultCalendula is the CalDAV client the tool calls.
	defaultCalendula = "calendula"
	// defaultExpand is the script that expands recurrences. It sits beside
	// this tool's own scripts, on the same PATH.
	defaultExpand = "expand-recurrences"
	// defaultTimezone interprets a calendar without a zone of its own.
	defaultTimezone = "UTC"
	// defaultState is where the fired alarms are remembered, relative to the
	// working directory unless the file names an absolute path.
	defaultState = "calendar-state.json"
	// defaultHorizonDays bounds how far ahead the calendar is expanded.
	defaultHorizonDays = 70
)

// Config is what the tool needs to read a calendar.
type Config struct {
	// CalendulaBin is the CalDAV client.
	CalendulaBin string `toml:"calendula_bin"`
	// CalendulaConfig is that client's own configuration file.
	CalendulaConfig string `toml:"calendula_config"`
	// ExpandScript expands a recurrence into occurrences.
	ExpandScript string `toml:"expand_script"`
	// Calendars are the calendar names to read.
	Calendars []string `toml:"calendars"`
	// Timezone interprets the wall clocks the calendar holds.
	Timezone string `toml:"timezone"`
	// State remembers the alarms already delivered.
	State string `toml:"state"`
	// HorizonDays bounds how far ahead the tool expands.
	HorizonDays int `toml:"horizon_days"`

	loc *time.Location
}

// loadConfig reads the configuration the profile ships beside this tool.
func loadConfig(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		CalendulaBin: defaultCalendula,
		ExpandScript: defaultExpand,
		Timezone:     defaultTimezone,
		State:        defaultState,
		HorizonDays:  defaultHorizonDays,
	}
	if err = toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(cfg.Calendars) == 0 {
		return nil, fmt.Errorf("%s: calendars is empty", path)
	}
	if cfg.CalendulaConfig == "" {
		return nil, fmt.Errorf("%s: calendula_config is empty", path)
	}
	if cfg.loc, err = time.LoadLocation(cfg.Timezone); err != nil {
		return nil, fmt.Errorf("%s: timezone %q: %w", path, cfg.Timezone, err)
	}
	if cfg.HorizonDays < 1 {
		return nil, fmt.Errorf("%s: horizon_days is %d, want at least 1", path, cfg.HorizonDays)
	}
	return cfg, nil
}

// defaultConfigPath returns the configuration the daemon's profile ships,
// `$VBCDD_HARMONY_PROFILE_DIR/calendar.toml`.
func defaultConfigPath() string {
	dir := os.Getenv("VBCDD_HARMONY_PROFILE_DIR")
	if dir == "" {
		return configName
	}
	return filepath.Join(dir, configName)
}

// calendar builds the reader from the configuration.
func (c *Config) calendar() *Calendar {
	return &Calendar{
		Bin:       c.CalendulaBin,
		Config:    c.CalendulaConfig,
		Expand:    c.ExpandScript,
		Calendars: c.Calendars,
		Loc:       c.loc,
		StateDir:  os.TempDir(),
	}
}
