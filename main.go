// Command vbcdd-harmony serves the profiles a configuration names, and offers
// the commands a tool or an operator needs beside it: `handoff` and `clear`,
// which reach a running daemon, and `install-profile`, which lays a profile
// down from a repository.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vbcdd.kafo.dev/harmony/internal/bridge"
	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/config"
	"vbcdd.kafo.dev/harmony/internal/profile"
	"vbcdd.kafo.dev/harmony/internal/provider"
	"vbcdd.kafo.dev/harmony/internal/xdg"
)

// appName is the directory the program owns below each XDG base directory, and
// the name of the tool.
const appName = "harmony"

// logLevelVariable names the environment variable that sets the log level.
const logLevelVariable = "VBCDD_HARMONY_LOG_LEVEL"

// toolTimeout bounds one hook, job, or tool call.
const toolTimeout = 20 * time.Minute

func main() {
	logger := newLogger()
	if err := run(logger, os.Args[1:]); err != nil {
		logger.Error("vbcdd-harmony", "error", err)
		os.Exit(1)
	}
}

// run dispatches one invocation. A first argument that is not a flag names the
// command, and no argument at all serves.
func run(logger *slog.Logger, args []string) error {
	name := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	switch name {
	case "serve":
		return serve(logger, args)
	case "handoff":
		return control(logger, "handoff", args)
	case "clear":
		return control(logger, "clear", args)
	case "install-profile":
		return install(logger, args)
	default:
		return fmt.Errorf("unknown command %q", name)
	}
}

// paths are the directories the program owns.
type paths struct {
	config   string
	data     string
	bin      string
	profiles string
	socket   string
}

// resolve returns the directories, from the environment or their XDG defaults.
func resolve() (paths, error) {
	configDir, err := xdg.Dir(xdg.Config, appName)
	if err != nil {
		return paths{}, err
	}
	dataDir, err := xdg.Dir(xdg.Data, appName)
	if err != nil {
		return paths{}, err
	}
	cacheDir, err := xdg.Dir(xdg.Cache, appName)
	if err != nil {
		return paths{}, err
	}
	return paths{
		config:   configDir,
		data:     dataDir,
		bin:      filepath.Join(cacheDir, "bin"),
		profiles: filepath.Join(configDir, "profiles"),
		socket:   bridge.SocketPath(dataDir),
	}, nil
}

// serve runs the daemon until it is interrupted.
func serve(logger *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to the config file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	dirs, err := resolve()
	if err != nil {
		return err
	}
	if *configPath == "" {
		*configPath = filepath.Join(dirs.config, "config.toml")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	profiles, err := profile.LoadAll(dirs.profiles)
	if err != nil {
		return err
	}
	if err = cfg.Validate(names(profiles)); err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	model, err := provider.NewModel(ctx, *cfg)
	if err != nil {
		return err
	}
	daemon, err := bridge.New(bridge.Config{
		DisplayName:     cfg.DisplayName,
		NtfyURL:         cfg.NtfyURL,
		NtfyToken:       cfg.NtfyToken,
		Model:           model,
		ProviderOptions: provider.Options(*cfg),
		MaxTurns:        cfg.MaxTurns,
		Topics:          cfg.Topics,
		Profiles:        profiles,
		DataDir:         dirs.data,
		BinDir:          dirs.bin,
		SocketPath:      dirs.socket,
		Runner:          command.Runner{Timeout: toolTimeout, Log: logger},
		Loc:             loc,
		Log:             logger,
	})
	if err != nil {
		return err
	}
	go func() {
		if listenErr := daemon.Listen(ctx, dirs.socket); listenErr != nil {
			logger.Error("listen", "error", listenErr)
			stop()
		}
	}()
	logger.Info("serving", "profiles", len(profiles), "socket", dirs.socket)
	return daemon.Run(ctx)
}

// control sends one request to a running daemon. The profile comes from the
// argument, or from the environment when a tool the daemon started asks.
func control(logger *slog.Logger, verb string, args []string) error {
	name := os.Getenv(bridge.EnvProfile)
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		return fmt.Errorf("no profile named: pass one, or run this from a tool the daemon started")
	}
	path := os.Getenv(bridge.EnvSocket)
	if path == "" {
		dirs, err := resolve()
		if err != nil {
			return err
		}
		path = dirs.socket
	}
	if err := bridge.Request(context.Background(), path, verb, name); err != nil {
		return fmt.Errorf("%s %s: %w", verb, name, err)
	}
	logger.Info(verb, "profile", name)
	return nil
}

// names returns the profile names, for a message about a bad topic map.
func names(profiles []profile.Profile) []string {
	names := make([]string, 0, len(profiles))
	for _, declared := range profiles {
		names = append(names, declared.Name)
	}
	return names
}

// newLogger writes the diagnostics to standard error, at the level the
// environment names.
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(os.Getenv(logLevelVariable))); err != nil {
		level = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// copyTree copies a directory into another, keeping the mode of every file.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

// copyFile copies one file, creating its parent directory.
func copyFile(from, to string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer target.Close()
	if _, err = io.Copy(target, source); err != nil {
		return err
	}
	return target.Close()
}
