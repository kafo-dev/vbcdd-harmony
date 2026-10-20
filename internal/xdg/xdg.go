// Package xdg resolves the directories a vbcdd tool owns below the XDG base
// directories, from the environment when it names one and from the XDG default
// otherwise.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

// Kind names one of the three XDG base directory variables.
type Kind string

// The base directories a tool can own.
const (
	// Config holds what the user wrote and would have to write again.
	Config Kind = "CONFIG"
	// Data holds what a delete would cost the user.
	Data Kind = "DATA"
	// Cache holds what a rebuild reproduces.
	Cache Kind = "CACHE"
)

// fallbackDir maps a kind to its XDG default below the home directory, which
// applies when the matching variable is unset.
var fallbackDir = map[Kind]string{
	Config: ".config",
	Data:   filepath.Join(".local", "share"),
	Cache:  ".cache",
}

// Dir returns $XDG_<KIND>_HOME/vbcdd/<app>, falling back to
// ~/.config/vbcdd/<app>, ~/.local/share/vbcdd/<app>, or ~/.cache/vbcdd/<app>
// when the variable is unset.
func Dir(kind Kind, app string) (string, error) {
	fallback, ok := fallbackDir[kind]
	if !ok {
		return "", fmt.Errorf("xdg: unknown kind %q", kind)
	}
	if base := os.Getenv("XDG_" + string(kind) + "_HOME"); base != "" {
		return filepath.Join(base, "vbcdd", app), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("xdg: home directory: %w", err)
	}
	return filepath.Join(home, fallback, "vbcdd", app), nil
}
