package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"

	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/hook"
	"vbcdd.kafo.dev/harmony/internal/profile"
	"vbcdd.kafo.dev/harmony/internal/tool"
	"vbcdd.kafo.dev/harmony/internal/transcript"
)

func TestDue(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	schedule := profile.Schedule{Time: "05:32"}
	now := time.Date(2026, 10, 20, 6, 0, 0, 0, loc)
	tomorrow := time.Date(2026, 10, 21, 5, 32, 0, 0, loc)

	cases := []struct {
		name    string
		stored  time.Time
		ok      bool
		fired   bool
		next    time.Time
		changed bool
	}{
		{"nothing stored", time.Time{}, false, false, tomorrow, true},
		{"still ahead", time.Date(2026, 10, 21, 5, 32, 0, 0, loc), true, false, tomorrow, false},
		{"passed", time.Date(2026, 10, 20, 5, 32, 0, 0, loc), true, true, tomorrow, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := due(schedule, tc.stored, tc.ok, now, loc)
			if err != nil {
				t.Fatalf("due: %v", err)
			}
			if got.fired != tc.fired {
				t.Errorf("fired = %v, want %v", got.fired, tc.fired)
			}
			if !got.next.Equal(tc.next) {
				t.Errorf("next = %s, want %s", got.next, tc.next)
			}
			if got.changed != tc.changed {
				t.Errorf("changed = %v, want %v", got.changed, tc.changed)
			}
		})
	}
}

func TestDueReportsABadSchedule(t *testing.T) {
	t.Parallel()
	_, err := due(profile.Schedule{Time: "5:32"}, time.Time{}, false, time.Now(), time.UTC)
	if err == nil {
		t.Fatal("due accepted a time that is not HH:MM")
	}
}

func TestStateSurvivesAReload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	state, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	jobAt := time.Date(2026, 10, 20, 5, 32, 0, 0, time.UTC)
	handoffAt := time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	if err = state.setJob("general/digest", jobAt); err != nil {
		t.Fatalf("setJob: %v", err)
	}
	if err = state.setHandoff("general", handoffAt); err != nil {
		t.Fatalf("setHandoff: %v", err)
	}

	reloaded, err := loadState(path)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	got, ok := reloaded.job("general/digest")
	if !ok || !got.Equal(jobAt) {
		t.Errorf("job = %s %v, want %s", got, ok, jobAt)
	}
	got, ok = reloaded.handoff("general")
	if !ok || !got.Equal(handoffAt) {
		t.Errorf("handoff = %s %v, want %s", got, ok, handoffAt)
	}
	if _, ok = reloaded.job("general/absent"); ok {
		t.Error("job found an absent entry")
	}
}

func TestLoadStateAcceptsAMissingFile(t *testing.T) {
	t.Parallel()
	state, err := loadState(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if _, ok := state.job("anything"); ok {
		t.Error("a fresh state has an entry")
	}
}

func TestLoadStateRejectsDamage(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), stateMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := loadState(path); err == nil {
		t.Fatal("loadState accepted a damaged file")
	}
}

func TestTopicOf(t *testing.T) {
	t.Parallel()
	topics := map[string]string{"vbcdd-general": "general", "vbcdd-harmony": "scheduler"}
	topic, err := topicOf(topics, "scheduler")
	if err != nil {
		t.Fatalf("topicOf: %v", err)
	}
	if topic != "vbcdd-harmony" {
		t.Fatalf("topic = %q", topic)
	}
	if _, err = topicOf(topics, "absent"); err == nil {
		t.Fatal("topicOf found an unrouted profile")
	}
}

func TestSystemPromptJoinsTheDocs(t *testing.T) {
	t.Parallel()
	got := systemPrompt(profile.Profile{
		SystemPrompt: "be brief",
		Docs:         []profile.Doc{{Name: "a.md", Body: "first"}, {Name: "b.md", Body: "second"}},
	})
	want := "be brief\n\nfirst\n\nsecond"
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
}

func TestWithAttachment(t *testing.T) {
	t.Parallel()
	got := withAttachment("what is this?", "/tmp/a.pdf", "application/pdf")
	if !strings.Contains(got, "/tmp/a.pdf") || !strings.Contains(got, "application/pdf") {
		t.Fatalf("prompt = %q, want the path and the type", got)
	}
	if !strings.HasSuffix(got, "what is this?") {
		t.Fatalf("prompt = %q, want the message last", got)
	}
	bare := withAttachment("   ", "/tmp/a.pdf", "")
	if !strings.Contains(bare, "/tmp/a.pdf") || strings.Contains(bare, "\n\n") {
		t.Fatalf("prompt = %q, want only the note", bare)
	}
}

// newBridge builds a bridge over one profile, which needs no endpoint until a
// turn runs.
func newBridge(t *testing.T, declared profile.Profile) *Bridge {
	t.Helper()
	b, err := New(Config{
		DisplayName: "vbcdd-harmony",
		Topics:      map[string]string{"vbcdd-general": declared.Name},
		Profiles:    []profile.Profile{declared},
		DataDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestNewRejectsAnUnroutedProfile(t *testing.T) {
	t.Parallel()
	_, err := New(Config{
		Profiles: []profile.Profile{{Name: "general"}},
		DataDir:  t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "no topic routes") {
		t.Fatalf("New error = %v, want a complaint about the topic", err)
	}
}

func TestNewSkipsADisabledProfile(t *testing.T) {
	t.Parallel()
	declared, err := profile.Load(writeProfile(t, "enabled = false\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := newBridge(t, declared)
	if len(b.services) != 0 {
		t.Fatalf("services = %d, want none", len(b.services))
	}
}

func TestClearDiscardsTheConversation(t *testing.T) {
	t.Parallel()
	dir := writeProfile(t, "")
	declared, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dataDir := t.TempDir()
	b, err := New(Config{
		Topics:   map[string]string{"vbcdd-general": "general"},
		Profiles: []profile.Profile{declared},
		DataDir:  dataDir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conversation := transcript.File{Path: transcriptPath(dataDir, "general")}
	if err = conversation.Append(); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err = b.Clear("general"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if err = b.Clear("absent"); err == nil {
		t.Fatal("Clear accepted an unknown profile")
	}
}

func TestHandoffNeedsADeclaration(t *testing.T) {
	t.Parallel()
	declared, err := profile.Load(writeProfile(t, ""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := newBridge(t, declared)
	if err = b.Handoff("general"); err == nil {
		t.Fatal("Handoff accepted a profile that declares none")
	}
}

func TestSocketRoundTrip(t *testing.T) {
	t.Parallel()
	dir := writeProfile(t, "[handoff]\ntokens = 10\n")
	if err := os.WriteFile(filepath.Join(dir, profile.HandoffName), []byte("write it down"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	declared, err := profile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b := newBridge(t, declared)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := SocketPath(t.TempDir())
	done := make(chan error, 1)
	go func() { done <- b.Listen(ctx, path) }()
	waitForSocket(t, path)

	if err = Request(ctx, path, "handoff", "general"); err != nil {
		t.Fatalf("Request handoff: %v", err)
	}
	if err = Request(ctx, path, "clear", "general"); err != nil {
		t.Fatalf("Request clear: %v", err)
	}
	if err = Request(ctx, path, "handoff", "absent"); err == nil {
		t.Fatal("Request accepted an unknown profile")
	}
	if err = Request(ctx, path, "wobble", "general"); err == nil {
		t.Fatal("Request accepted an unknown verb")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatalf("Listen: %v", err)
	}
}

func TestRequestReportsAMissingDaemon(t *testing.T) {
	t.Parallel()
	path := SocketPath(t.TempDir())
	if err := Request(context.Background(), path, "clear", "general"); err == nil {
		t.Fatal("Request reached a daemon that is not there")
	}
}

// waitForSocket blocks until the listener has bound the socket.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the socket %s never appeared", path)
}

// writeProfile lays out one profile directory and returns its path.
func writeProfile(t *testing.T, declaration string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "general")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, profile.FileName), []byte(declaration), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, profile.PromptName), []byte("be brief"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

// TestAToolRunsInTheProfileWorkDirectory guards the wiring: a declared tool
// must run through the profile's own runner, so its target resolves on the
// profile's PATH and its working directory is the profile's work directory.
func TestAToolRunsInTheProfileWorkDirectory(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	profileDir := t.TempDir()
	toolsDir := filepath.Join(profileDir, "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	body := "#!/bin/sh\nexec /bin/sh -c \"$VBCDD_ARG_CMD\"\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "bash"), []byte(body), 0o700); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	spec, err := tool.Parse([]byte("description = \"run\"\nargs = { cmd = \"string\" }\ntarget = \"bash\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	declared := profile.Profile{
		Name:  "general",
		Dir:   profileDir,
		Tools: []profile.Tool{{Name: "bash", Spec: spec}},
	}

	turns := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		turns++
		if turns == 1 {
			sse(t, w, map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "bash", "arguments": `{"cmd":"touch marker"}`},
			}}}, "tool_calls")
			return
		}
		sse(t, w, map[string]any{"content": "done"}, "stop")
	}))
	defer server.Close()

	state, err := loadState(filepath.Join(dataDir, "state.json"))
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	cfg := Config{
		DisplayName: "t",
		Model:       bridgeModel(t, server),
		Topics:      map[string]string{"topic": "general"},
		Profiles:    []profile.Profile{declared},
		DataDir:     dataDir,
		BinDir:      filepath.Join(t.TempDir(), "bin"),
		SocketPath:  filepath.Join(dataDir, "harmony.sock"),
		Runner:      command.Runner{},
		Loc:         time.UTC,
	}
	svc, err := newService(cfg, declared, state, time.Now, time.UTC)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	if _, err = svc.turn(context.Background(), hook.Run{
		Prompt: "go", Reason: "message", Topic: "topic", Profile: "general",
	}); err != nil {
		t.Fatalf("turn: %v", err)
	}
	marker := filepath.Join(dataDir, "profiles", "general", "work", "marker")
	if _, err = os.Stat(marker); err != nil {
		t.Fatalf("the tool did not run in the work directory: %v", err)
	}
}

// bridgeModel points a language model at a fake endpoint.
func bridgeModel(t *testing.T, server *httptest.Server) fantasy.LanguageModel {
	t.Helper()
	provider, err := openaicompat.New(
		openaicompat.WithBaseURL(server.URL),
		openaicompat.WithAPIKey("key"),
	)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	lm, err := provider.LanguageModel(context.Background(), "model")
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	return lm
}

// sse streams one chunk carrying delta and a finish reason.
func sse(t *testing.T, w http.ResponseWriter, delta map[string]any, finish any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion.chunk", "created": 1, "model": "model",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", raw)
}
