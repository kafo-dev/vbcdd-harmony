package xdg

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDirUsesTheEnvironment(t *testing.T) {
	cases := []struct {
		kind     Kind
		variable string
		base     string
	}{
		{Config, "XDG_CONFIG_HOME", "/base/conf"},
		{Data, "XDG_DATA_HOME", "/base/data"},
		{Cache, "XDG_CACHE_HOME", "/base/cache"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Setenv(tc.variable, tc.base)
			got, err := Dir(tc.kind, "harmony")
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			want := filepath.Join(tc.base, "vbcdd", "harmony")
			if got != want {
				t.Fatalf("Dir = %q, want %q", got, want)
			}
		})
	}
}

func TestDirFallsBackToTheXDGDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		kind     Kind
		variable string
		fallback string
	}{
		{Config, "XDG_CONFIG_HOME", ".config"},
		{Data, "XDG_DATA_HOME", filepath.Join(".local", "share")},
		{Cache, "XDG_CACHE_HOME", ".cache"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Setenv(tc.variable, "")
			got, err := Dir(tc.kind, "harmony")
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			want := filepath.Join(home, tc.fallback, "vbcdd", "harmony")
			if got != want {
				t.Fatalf("Dir = %q, want %q", got, want)
			}
		})
	}
}

func TestDirRejectsAnUnknownKind(t *testing.T) {
	t.Parallel()
	_, err := Dir(Kind("STATE"), "harmony")
	if err == nil || !strings.Contains(err.Error(), "STATE") {
		t.Fatalf("Dir error = %v, want a complaint about the kind", err)
	}
}
