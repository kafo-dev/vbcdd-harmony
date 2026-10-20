package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// installUsage is what an invocation without a URL prints.
const installUsage = "usage: vbcdd-harmony install-profile <git-url>"

// installTimeout bounds the clone and the build of one installation.
const installTimeout = 10 * time.Minute

// install lays a profile set down from a repository: it clones the repository,
// copies every profile into the configuration directory, and builds the tools
// the profiles name into the cache, where every profile's children reach them
// on their PATH.
func install(logger *slog.Logger, args []string) error {
	if len(args) != 1 {
		return errors.New(installUsage)
	}
	url := args[0]
	dirs, err := resolve()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()

	clone, err := os.MkdirTemp("", "vbcdd-harmony-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(clone)

	if err = cloneRepository(ctx, url, clone); err != nil {
		return err
	}
	installed, err := installProfiles(clone, dirs.profiles)
	if err != nil {
		return err
	}
	built, err := buildTools(ctx, clone, dirs.bin)
	if err != nil {
		return err
	}
	logger.Info("installed", "profiles", installed, "tools", built)
	return nil
}

// cloneRepository fetches the repository into an empty directory.
func cloneRepository(ctx context.Context, url, dir string) error {
	//nolint:gosec // the URL is the operator's own argument.
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone %s: %w", url, err)
	}
	return nil
}

// installProfiles copies every profile of the repository into the
// configuration directory, and returns how many it laid down.
func installProfiles(clone, profilesDir string) (int, error) {
	source := filepath.Join(clone, "profiles")
	entries, err := os.ReadDir(source)
	if err != nil {
		return 0, fmt.Errorf("the repository carries no profiles: %w", err)
	}
	installed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		from := filepath.Join(source, entry.Name())
		if err = copyTree(from, filepath.Join(profilesDir, entry.Name())); err != nil {
			return installed, err
		}
		installed++
	}
	return installed, nil
}

// buildTools builds every command below the repository's `cmd` directory into
// the cache, and returns how many it built.
func buildTools(ctx context.Context, clone, binDir string) (int, error) {
	source := filepath.Join(clone, "cmd")
	entries, err := os.ReadDir(source)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if err = os.MkdirAll(binDir, 0o700); err != nil {
		return 0, err
	}
	built := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		out := filepath.Join(binDir, name)
		cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/"+name)
		cmd.Dir = clone
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			return built, fmt.Errorf("build %s: %w", name, err)
		}
		built++
	}
	return built, nil
}
