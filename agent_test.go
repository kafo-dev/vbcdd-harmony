package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptSections(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"You are the calendar and mail assistant",
		"reached through ntfy",
		"ntfy renders replies as Markdown",
		"current local time",
		"`media/`",
		"`outbox/`",
		"## Chat Style",
		"# Remembering the user",
		"## Tooling requests",
	} {
		if !strings.Contains(Prompt, want) {
			t.Errorf("Prompt is missing %q", want)
		}
	}
	if strings.Contains(Prompt, "org.matrix.custom.html") {
		t.Error("Prompt still mentions Matrix HTML")
	}
}

func TestTimeContext(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		Loc: loc,
		Now: func() time.Time { return time.Date(2026, 10, 9, 20, 31, 0, 0, time.UTC) },
	}
	got := a.timeContext()
	for _, want := range []string{"2026-10-10 05:31", "Asia/Tokyo", "20:31 UTC"} {
		if !strings.Contains(got, want) {
			t.Errorf("timeContext missing %q: %q", want, got)
		}
	}
}

func TestTurnArgsSandbox(t *testing.T) {
	t.Parallel()
	a := &Agent{Bin: "/opt/pi", Sandbox: "/usr/local/bin/vbcdd-sandbox", Session: "s"}
	bin, args := a.turnArgs("hi")
	if bin != "/usr/local/bin/vbcdd-sandbox" {
		t.Errorf("bin = %q", bin)
	}
	if len(args) == 0 || args[0] != "/opt/pi" {
		t.Fatalf("args[0] = %v", args)
	}
	if last := args[len(args)-1]; last != "hi" {
		t.Errorf("last arg = %q", last)
	}

	plain := &Agent{Bin: "/opt/pi", Session: "s"}
	if plainBin, _ := plain.turnArgs("hi"); plainBin != "/opt/pi" {
		t.Errorf("plain bin = %q", plainBin)
	}
}

func TestSessionDir(t *testing.T) {
	t.Parallel()
	a := &Agent{Home: "/home/vbcdd", Dir: "/home/vbcdd/.local/share/vbcdd/harmony", Session: "vbcdd-harmony"}
	got, err := a.SessionDir()
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/vbcdd/.pi/agent/sessions/--home-vbcdd-.local-share-vbcdd-harmony--"
	if got != want {
		t.Errorf("SessionDir = %q, want %q", got, want)
	}
}

func TestClearTranscript(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Home: home, Dir: state, Session: "s"}
	dir, err := a.SessionDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "2026-01-01T00-00-00-000Z_s.jsonl")
	other := filepath.Join(dir, "2026-01-01T00-00-00-000Z_other.jsonl")
	for _, path := range []string{mine, other} {
		if err = os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := a.ClearTranscript()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, statErr := os.Stat(mine); !os.IsNotExist(statErr) {
		t.Error("the session transcript survived")
	}
	if _, statErr := os.Stat(other); statErr != nil {
		t.Errorf("another session's transcript was removed: %v", statErr)
	}

	if again, clearErr := a.ClearTranscript(); clearErr != nil || again != 0 {
		t.Errorf("second clear = %d, %v; want 0, nil", again, clearErr)
	}
}
