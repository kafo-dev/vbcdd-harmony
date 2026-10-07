package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("vbcdd-harmony", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfgPath := flag.String(
		"config",
		"",
		"path to the config file (default: $XDG_CONFIG_HOME/vbcdd/harmony/config.toml)",
	)
	flag.Parse()

	if *cfgPath == "" {
		*cfgPath = defaultConfigPath()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ntfy := NewNtfy(cfg)

	cal := &Calendar{
		Bin:       cfg.CalendulaBin,
		Config:    cfg.CalendulaConfig,
		Expand:    cfg.ExpandScript,
		Calendars: cfg.Calendars,
		Loc:       cfg.loc,
		StateDir:  cfg.StateDir,
	}
	agent := &Agent{
		Bin:      cfg.PiBin,
		Provider: cfg.PiProvider,
		Model:    cfg.PiModel,
		Session:  cfg.PiSession,
		Tools:    cfg.PiTools,
		Dir:      cfg.StateDir,
		Home:     os.Getenv("HOME"),
		Sandbox:  cfg.PiSandbox,
		Loc:      cfg.loc,
		Now:      time.Now,
	}

	return NewBridge(cfg, cal, agent, ntfy, logger, time.Now).Run(ctx)
}
