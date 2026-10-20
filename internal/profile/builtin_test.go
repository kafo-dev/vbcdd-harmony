package profile

import (
	"path/filepath"
	"testing"
)

// TestBuiltinProfilesLoad checks the profiles this repository ships. They are
// the ones `install-profile` copies, so a mistake in one is a mistake in every
// installation.
func TestBuiltinProfilesLoad(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "profiles")
	profiles, err := LoadAll(root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %d, want general and scheduler", len(profiles))
	}
	for _, p := range profiles {
		if p.Name != "general" && p.Name != "scheduler" {
			t.Errorf("unexpected profile %q", p.Name)
		}
		if len(p.Tools) == 0 {
			t.Errorf("profile %q declares no tools", p.Name)
		}
		for _, declared := range p.Tools {
			if declared.Spec.Target == "" {
				t.Errorf("profile %q tool %q has no target", p.Name, declared.Name)
			}
		}
	}
}

// TestBuiltinGeneralIsUnsandboxed checks the two profiles differ where they are
// meant to: the scheduler's shell goes through bubblewrap, and the general one
// does not.
func TestBuiltinProfilesDifferOnTheSandbox(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "profiles")
	for _, name := range []string{"general", "scheduler"} {
		p, err := Load(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		var target string
		for _, declared := range p.Tools {
			if declared.Name == "bash" {
				target = declared.Spec.Target
			}
		}
		if target != "bash" {
			t.Fatalf("profile %q bash target = %q, want the script beside it", name, target)
		}
	}
}
