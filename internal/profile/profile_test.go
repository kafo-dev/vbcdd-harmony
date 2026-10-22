package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write lays out one file, creating its parent directories.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// build lays out one profile directory and returns its path.
func build(t *testing.T, root, name, declaration string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	write(t, filepath.Join(dir, FileName), declaration)
	write(t, filepath.Join(dir, InstructionsDirName, "agent.md"), "be brief")
	return dir
}

const fullDeclaration = `
enabled = true

[[hooks]]
name = "time"
command = "time"
mode = "alter"

[[hooks]]
name = "clear"
command = "clear"
mode = "override"
match = "^/clear$"

[[jobs]]
name = "digest"
run = { time = "05:32" }
prompt = "Summarize today."

[[jobs]]
name = "alarms"
enabled = false
run = { interval_minutes = 10 }
next = "calendar next"
command = "notify"
`

func TestLoadReadsAProfile(t *testing.T) {
	t.Parallel()
	dir := build(t, t.TempDir(), "scheduler", fullDeclaration)
	write(t, filepath.Join(dir, DocsDirName, "b.md"), "---\ndescription: second\n---\n# B\n")
	write(t, filepath.Join(dir, DocsDirName, "a.md"), "---\ndescription: first\n---\n# A\n")
	write(t, filepath.Join(dir, DocsDirName, "ignored.txt"), "no")
	write(t, filepath.Join(dir, ToolsDirName, "bash.toml"), "description = \"d\"\ntarget = \"bash\"\n")
	write(t, filepath.Join(dir, ToolsDirName, "calendar.toml"), "description = \"d\"\ntarget = \"cal\"\n")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Name != "scheduler" || p.Dir != dir {
		t.Errorf("name = %q, dir = %q", p.Name, p.Dir)
	}
	if strings.TrimSpace(p.Instructions) != "be brief" {
		t.Errorf("instructions = %q", p.Instructions)
	}
	if !p.On() {
		t.Error("On = false, want the declared true")
	}
	if len(p.Docs) != 2 || p.Docs[0].Name != "a.md" || p.Docs[0].Description != "first" ||
		p.Docs[1].Name != "b.md" || p.Docs[1].Description != "second" {
		t.Fatalf("docs = %+v, want a.md then b.md with their descriptions", p.Docs)
	}
	if want := filepath.Join(dir, DocsDirName, "a.md"); p.Docs[0].Path != want {
		t.Errorf("doc path = %q, want %q", p.Docs[0].Path, want)
	}
	if len(p.Tools) != 2 || p.Tools[0].Name != "bash" || p.Tools[1].Name != "calendar" {
		t.Fatalf("tools = %+v, want bash then calendar", p.Tools)
	}
	if len(p.Hooks) != 2 || p.Hooks[0].Mode != Alter || p.Hooks[1].Mode != Override {
		t.Fatalf("hooks = %+v", p.Hooks)
	}
	if len(p.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(p.Jobs))
	}
	if !p.Jobs[0].On() || p.Jobs[1].On() {
		t.Error("job enablement did not follow the declaration")
	}
}

func TestLoadAcceptsAMinimalProfile(t *testing.T) {
	t.Parallel()
	dir := build(t, t.TempDir(), "plain", "")
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !p.On() {
		t.Error("On = false, want a profile that declares nothing to be on")
	}
	if p.Docs != nil || p.Tools != nil || p.Hooks != nil || p.Jobs != nil {
		t.Errorf("optional parts = %+v, want none", p)
	}
}

func TestLoadJoinsTheInstructions(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "plain")
	write(t, filepath.Join(dir, FileName), "")
	write(t, filepath.Join(dir, InstructionsDirName, "b.md"), "second")
	write(t, filepath.Join(dir, InstructionsDirName, "a.md"), "first")
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Instructions != "first\n\nsecond" {
		t.Fatalf("instructions = %q, want the two files joined in name order", p.Instructions)
	}
}

func TestLoadAllSkipsWhatIsNotAProfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	build(t, root, "scheduler", "")
	build(t, root, "general", "")
	// A name outside the profile pattern, a stray file, and a directory
	// without a declaration.
	write(t, filepath.Join(root, "README.md"), "not a profile")
	write(t, filepath.Join(root, ".hidden", FileName), "")

	profiles, err := LoadAll(root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(profiles) != 2 || profiles[0].Name != "general" || profiles[1].Name != "scheduler" {
		t.Fatalf("profiles = %+v, want general then scheduler", profiles)
	}
}

func TestLoadAllReportsAMissingRoot(t *testing.T) {
	t.Parallel()
	if _, err := LoadAll(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("LoadAll accepted a missing root")
	}
}

func TestLoadRejectsBadProfiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dir  string
		prep func(t *testing.T, dir string)
		want string
	}{
		{"bad directory name", "Scheduler", nil, "not a valid profile name"},
		{"missing declaration", "plain", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Remove(filepath.Join(dir, FileName)); err != nil {
				t.Fatal(err)
			}
		}, "profile.toml"},
		{"doc without frontmatter", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, DocsDirName, "a.md"), "# A\n")
		}, "no frontmatter"},
		{"doc without a description", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, DocsDirName, "a.md"), "---\nname: a\n---\n# A\n")
		}, "no description"},
		{"doc with an empty description", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, DocsDirName, "a.md"), "---\ndescription:\n---\n# A\n")
		}, "description is empty"},
		{"invalid TOML", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, FileName), "enabled = ")
		}, "unexpected"},
		{"bad tool name", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, ToolsDirName, "Bad.toml"), "description = \"d\"\ntarget = \"t\"\n")
		}, "not a valid tool name"},
		{"bad tool declaration", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, ToolsDirName, "bash.toml"), "description = \"d\"\n")
		}, "target is empty"},
		{"hook without a command", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, FileName), "[[hooks]]\nname = \"h\"\nmode = \"alter\"\n")
		}, "command is empty"},
		{"hook with an unknown mode", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, FileName), "[[hooks]]\nname = \"h\"\ncommand = \"c\"\nmode = \"rewrite\"\n")
		}, "mode \"rewrite\""},
		{"hook with a bad pattern", "plain", func(t *testing.T, dir string) {
			t.Helper()
			declaration := "[[hooks]]\nname = \"h\"\ncommand = \"c\"\nmode = \"alter\"\nmatch = \"(\"\n"
			write(t, filepath.Join(dir, FileName), declaration)
		}, "match \"(\""},
		{"job without a name", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, FileName), "[[jobs]]\nrun = { time = \"05:32\" }\nprompt = \"p\"\n")
		}, "name is empty"},
		{"job with neither prompt nor command", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, FileName), "[[jobs]]\nname = \"j\"\nrun = { time = \"05:32\" }\n")
		}, "one of prompt or command is required"},
		{"job with both", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(
				t,
				filepath.Join(dir, FileName),
				"[[jobs]]\nname = \"j\"\nrun = { time = \"05:32\" }\nprompt = \"p\"\ncommand = \"c\"\n",
			)
		}, "prompt and command are exclusive"},
		{"job with no cadence", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(t, filepath.Join(dir, FileName), "[[jobs]]\nname = \"j\"\nrun = {}\nprompt = \"p\"\n")
		}, "neither time nor interval_minutes"},
		{"job with two cadences", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(
				t,
				filepath.Join(dir, FileName),
				"[[jobs]]\nname = \"j\"\nrun = { time = \"05:32\", interval_minutes = 10 }\nprompt = \"p\"\n",
			)
		}, "both interval_minutes and a time"},
		{"job with a bad time", "plain", func(t *testing.T, dir string) {
			t.Helper()
			write(
				t,
				filepath.Join(dir, FileName),
				"[[jobs]]\nname = \"j\"\nrun = { time = \"5:32\" }\nprompt = \"p\"\n",
			)
		}, "is not HH:MM"},
		{"job with a bad weekday", "plain", func(t *testing.T, dir string) {
			t.Helper()
			declaration := "[[jobs]]\nname = \"j\"\nrun = { time = \"05:32\", weekday = \"Funday\" }\nprompt = \"p\"\n"
			write(t, filepath.Join(dir, FileName), declaration)
		}, "is not a weekday abbreviation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := build(t, root, tc.dir, "")
			if tc.prep != nil {
				tc.prep(t, dir)
			}
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load accepted the profile")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestHookMatches(t *testing.T) {
	t.Parallel()
	dir := build(t, t.TempDir(), "plain", `[[hooks]]
name = "clear"
command = "clear"
mode = "override"
match = "^/clear"

[[hooks]]
name = "time"
command = "time"
mode = "alter"
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := []struct {
		hook string
		body string
		want bool
	}{
		{"clear", "/clear", true},
		{"clear", "/clear now", true},
		{"clear", "clear", false},
		{"time", "anything at all", true},
	}
	for _, tc := range cases {
		hook := p.Hooks[0]
		if tc.hook == "time" {
			hook = p.Hooks[1]
		}
		if got := hook.Matches(tc.body); got != tc.want {
			t.Errorf("%s hook against %q = %v, want %v", tc.hook, tc.body, got, tc.want)
		}
	}
}

func TestLoadReadsAHandoff(t *testing.T) {
	t.Parallel()
	dir := build(t, t.TempDir(), "general", "[handoff]\ntokens = 1000\nbytes = 4096\nafter = { time = \"04:00\" }\n")
	write(t, filepath.Join(dir, HandoffName), "write down what the next conversation needs")
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Handoff == nil {
		t.Fatal("Handoff = nil, want the declaration")
	}
	if p.Handoff.Tokens != 1000 || p.Handoff.Bytes != 4096 {
		t.Errorf("thresholds = %d %d", p.Handoff.Tokens, p.Handoff.Bytes)
	}
	if strings.TrimSpace(p.Handoff.Prompt) != "write down what the next conversation needs" {
		t.Errorf("prompt = %q", p.Handoff.Prompt)
	}
	if p.Handoff.After.Time != "04:00" {
		t.Errorf("after = %+v", p.Handoff.After)
	}
}

func TestHandoffDue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		handoff Handoff
		tokens  int
		bytes   int64
		want    bool
	}{
		{"nothing set", Handoff{After: Schedule{Time: "04:00"}}, 100000, 100000, false},
		{"under both", Handoff{Tokens: 100, Bytes: 200}, 99, 199, false},
		{"at the token count", Handoff{Tokens: 100}, 100, 0, true},
		{"over the token count", Handoff{Tokens: 100}, 101, 0, true},
		{"at the byte count", Handoff{Bytes: 200}, 0, 200, true},
		{"over the byte count", Handoff{Bytes: 200, Tokens: 100}, 1, 200, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.handoff.Due(tc.tokens, tc.bytes); got != tc.want {
				t.Fatalf("Due(%d, %d) = %v, want %v", tc.tokens, tc.bytes, got, tc.want)
			}
		})
	}
}

func TestLoadRejectsBadHandoffs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		handoff  string
		withFile bool
		want     string
	}{
		{"no threshold", "[handoff]\n", true, "names no threshold"},
		{"negative tokens", "[handoff]\ntokens = -1\n", true, "cannot be negative"},
		{"negative bytes", "[handoff]\nbytes = -1\n", true, "cannot be negative"},
		{"bad schedule", "[handoff]\nafter = { time = \"4:00\" }\n", true, "is not HH:MM"},
		{"missing instruction", "[handoff]\ntokens = 10\n", false, HandoffName},
		{"empty instruction", "[handoff]\ntokens = 10\n", true, "handoff instruction is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := build(t, t.TempDir(), "general", tc.handoff)
			if tc.withFile {
				body := "write it down"
				if tc.want == "handoff instruction is empty" {
					body = "   \n"
				}
				write(t, filepath.Join(dir, HandoffName), body)
			}
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load accepted the handoff")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}
