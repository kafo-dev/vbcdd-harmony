package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"

	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/tool"
	"vbcdd.kafo.dev/harmony/internal/transcript"
)

// model points a language model at a fake endpoint.
func model(t *testing.T, server *httptest.Server) fantasy.LanguageModel {
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

// conversation returns a transcript path inside a temporary directory.
func conversation(t *testing.T) transcript.File {
	t.Helper()
	return transcript.File{Path: filepath.Join(t.TempDir(), "conversation.jsonl")}
}

// headers sets the content type a streamed reply carries.
func headers(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
}

// write streams one server-sent event.
func write(t *testing.T, w http.ResponseWriter, event map[string]any) {
	t.Helper()
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode event: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", raw)
}

// chunk renders one streamed chunk carrying delta.
func chunk(delta map[string]any, finish any) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion.chunk",
		"created": 1,
		"model":   "model",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
}

// text streams an answer and ends the turn.
func text(t *testing.T, w http.ResponseWriter, body string, usage map[string]any) {
	t.Helper()
	write(t, w, chunk(map[string]any{"content": body}, nil))
	write(t, w, map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion.chunk",
		"created": 1,
		"model":   "model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   usage,
	})
}

// script writes an executable shell command into a temporary directory.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestAskRecordsTheConversation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		headers(w)
		text(t, w, "the answer", map[string]any{"prompt_tokens": 120, "completion_tokens": 5, "total_tokens": 125})
	}))
	defer server.Close()

	var streamed bytes.Buffer
	file := conversation(t)
	a, err := New(Config{
		Model:        model(t, server),
		Instructions: "be brief",
		MaxTurns:     4,
		Transcript:   file,
		OnText:       func(s string) { streamed.WriteString(s) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reply, err := a.Ask(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if reply != "the answer" {
		t.Fatalf("reply = %q", reply)
	}
	if streamed.String() != "the answer" {
		t.Fatalf("streamed = %q", streamed.String())
	}
	if got := a.Tokens(); got != 120 {
		t.Fatalf("Tokens = %d, want the reported 120", got)
	}
	size, err := a.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if size == 0 {
		t.Fatal("Bytes = 0, want a stored conversation")
	}
	stored, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored) != 3 || stored[0].Role != fantasy.MessageRoleSystem ||
		stored[1].Role != fantasy.MessageRoleUser || stored[2].Role != fantasy.MessageRoleAssistant {
		t.Fatalf("stored = %+v, want the instructions, the prompt, and the answer", stored)
	}
}

func TestNewResumesTheStoredConversation(t *testing.T) {
	t.Parallel()
	file := conversation(t)
	if err := file.Append(fantasy.NewUserMessage("earlier")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	a, err := New(Config{Transcript: file, MaxTurns: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := a.History(); len(got) != 1 {
		t.Fatalf("history = %d messages, want the stored one", len(got))
	}
}

func TestAskRunsATool(t *testing.T) {
	t.Parallel()
	runner := command.Runner{}
	spec, err := tool.Parse([]byte("description = \"run\"\nargs = { cmd = \"string\" }\ntarget = \"unused\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	spec.Target = script(t, `printf 'ran %s' "$VBCDD_ARG_CMD"`)
	declared, err := NewTool("bash", spec, runner)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}

	turns := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
			t.Errorf("decode request: %v", decodeErr)
		}
		turns++
		headers(w)
		if turns == 1 {
			write(t, w, chunk(map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0,
					"id":    "call_1",
					"type":  "function",
					"function": map[string]any{
						"name":      "bash",
						"arguments": `{"cmd":"ls"}`,
					},
				}},
			}, nil))
			write(t, w, map[string]any{
				"id": "chatcmpl-1", "object": "chat.completion.chunk", "created": 1, "model": "model",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
			})
			return
		}
		last := body.Messages[len(body.Messages)-1]
		if last.Role != "tool" {
			t.Errorf("last role = %q, want the tool result", last.Role)
		}
		text(t, w, "done", nil)
	}))
	defer server.Close()

	a, err := New(Config{
		Model:      model(t, server),
		Tools:      []fantasy.AgentTool{declared},
		MaxTurns:   4,
		Transcript: conversation(t),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reply, err := a.Ask(context.Background(), "list the files")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if reply != "done" {
		t.Fatalf("reply = %q", reply)
	}
	history := a.History()
	if len(history) != 4 {
		t.Fatalf("history = %d messages, want 4", len(history))
	}
	result, found := fantasy.AsMessagePart[fantasy.ToolResultPart](history[2].Content[0])
	if !found {
		t.Fatalf("third message = %+v, want the tool result", history[2])
	}
	output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](
		result.Output,
	)
	if !ok || output.Text != "ran ls" {
		t.Fatalf("tool output = %+v, want what the target printed", result.Output)
	}
}

func TestHandoffStartsAFreshConversation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		headers(w)
		text(t, w, "we were listing the files", nil)
	}))
	defer server.Close()

	file := conversation(t)
	if err := file.Append(fantasy.NewUserMessage("old message")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	a, err := New(Config{
		Model:      model(t, server),
		Handoff:    "write down what the next conversation needs",
		MaxTurns:   2,
		Transcript: file,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = a.Handoff(context.Background()); err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	history := a.History()
	if len(history) != 1 {
		t.Fatalf("history = %d messages, want only the summary", len(history))
	}
	seed, ok := fantasy.AsMessagePart[fantasy.TextPart](history[0].Content[0])
	if !ok || seed.Text != "we were listing the files" {
		t.Fatalf("seed = %+v, want the summary", history[0])
	}
	stored, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored = %d messages, want the conversation replaced", len(stored))
	}
}

func TestHandoffReinsertsTheInstructions(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		headers(w)
		text(t, w, "we were listing the files", nil)
	}))
	defer server.Close()

	file := conversation(t)
	if err := file.Append(fantasy.NewUserMessage("old message")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	a, err := New(Config{
		Model:        model(t, server),
		Instructions: "be brief",
		Handoff:      "write down what the next conversation needs",
		MaxTurns:     2,
		Transcript:   file,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = a.Handoff(context.Background()); err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	history := a.History()
	if len(history) != 2 || history[0].Role != fantasy.MessageRoleSystem ||
		history[1].Role != fantasy.MessageRoleUser {
		t.Fatalf("history = %+v, want the instructions then the summary", history)
	}
}

func TestHandoffWithoutAPrompt(t *testing.T) {
	t.Parallel()
	a, err := New(Config{Transcript: conversation(t), MaxTurns: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = a.Handoff(context.Background()); err == nil {
		t.Fatal("Handoff ran without a prompt")
	}
}

func TestResetDiscardsTheConversation(t *testing.T) {
	t.Parallel()
	file := conversation(t)
	if err := file.Append(fantasy.NewUserMessage("gone")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	a, err := New(Config{Transcript: file, MaxTurns: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = a.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(a.History()) != 0 {
		t.Fatalf("history = %d messages, want none", len(a.History()))
	}
	if a.Tokens() != 0 {
		t.Fatalf("Tokens = %d, want none", a.Tokens())
	}
}

func TestTokensFallsBackToTheRule(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		headers(w)
		text(t, w, strings.Repeat("a", 40), nil)
	}))
	defer server.Close()

	a, err := New(Config{Model: model(t, server), MaxTurns: 1, Transcript: conversation(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err = a.Ask(context.Background(), strings.Repeat("a", 40)); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := a.Tokens(); got != 20 {
		t.Fatalf("Tokens = %d, want 20 from 80 characters", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	t.Parallel()
	messages := []fantasy.Message{{
		Role: fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: strings.Repeat("a", 4)},
			fantasy.ReasoningPart{Text: strings.Repeat("b", 4)},
			fantasy.ToolCallPart{ToolCallID: "c", ToolName: "ab", Input: "cd"},
			fantasy.ToolResultPart{
				ToolCallID: "c",
				Output:     fantasy.ToolResultOutputContentText{Text: strings.Repeat("e", 8)},
			},
			fantasy.FilePart{},
		},
	}}
	if got := estimateTokens(messages); got != 5 {
		t.Fatalf("estimateTokens = %d, want 20 characters over 4", got)
	}
}

func TestJSONType(t *testing.T) {
	t.Parallel()
	cases := map[tool.ArgType]string{
		tool.String:         "string",
		tool.Int:            "integer",
		tool.Float:          "number",
		tool.Bool:           "boolean",
		tool.ArgType("odd"): "string",
	}
	for kind, want := range cases {
		if got := jsonType(kind); got != want {
			t.Errorf("jsonType(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestNewToolBuildsTheSchema(t *testing.T) {
	t.Parallel()
	spec, err := tool.Parse([]byte(`
description = "Run a shell command."
args = { cmd = "string", timeout = "int?" }
target = "bash"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	declared, err := NewTool("bash", spec, command.Runner{})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	info := declared.Info()
	if info.Name != "bash" || info.Description != "Run a shell command." {
		t.Fatalf("info = %+v", info)
	}
	if len(info.Required) != 1 || info.Required[0] != "cmd" {
		t.Fatalf("required = %v, want only cmd", info.Required)
	}
	properties, ok := info.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters = %#v", info.Parameters)
	}
	if got := properties["timeout"]; got == nil {
		t.Fatalf("properties = %#v, want timeout declared", properties)
	}
}
