package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
)

// path returns a conversation path inside a temporary directory.
func path(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "conversation.jsonl")
}

// sample is one conversation with a tool call and its result.
func sample() []fantasy.Message {
	return []fantasy.Message{
		fantasy.NewUserMessage("list the files"),
		{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
			fantasy.ToolCallPart{ToolCallID: "call_1", ToolName: "bash", Input: `{"cmd":"ls"}`},
		}},
		{Role: fantasy.MessageRoleTool, Content: []fantasy.MessagePart{
			fantasy.ToolResultPart{
				ToolCallID: "call_1",
				Output:     fantasy.ToolResultOutputContentText{Text: "main.go"},
			},
		}},
		{Role: fantasy.MessageRoleAssistant, Content: []fantasy.MessagePart{
			fantasy.TextPart{Text: "the only Go file is main.go"},
		}},
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	file := File{Path: path(t)}
	sent := sample()
	if err := file.Append(sent...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(sent) {
		t.Fatalf("messages = %d, want %d", len(got), len(sent))
	}
	if got[0].Role != fantasy.MessageRoleUser {
		t.Errorf("first role = %q", got[0].Role)
	}
	call, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](got[1].Content[0])
	if !ok || call.ToolName != "bash" {
		t.Fatalf("second message = %+v, want the tool call", got[1])
	}
	result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](got[2].Content[0])
	if !ok || result.ToolCallID != "call_1" {
		t.Fatalf("third message = %+v, want the tool result", got[2])
	}
	text, ok := fantasy.AsMessagePart[fantasy.TextPart](got[3].Content[0])
	if !ok || text.Text != "the only Go file is main.go" {
		t.Fatalf("last message = %+v, want the assistant's text", got[3])
	}
}

func TestAppendAddsToTheEnd(t *testing.T) {
	t.Parallel()
	file := File{Path: path(t)}
	if err := file.Append(fantasy.NewUserMessage("first")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	assistant := fantasy.Message{
		Role:    fantasy.MessageRoleAssistant,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: "second"}},
	}
	if err := file.Append(assistant); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2", len(got))
	}
}

func TestLoadOfAMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	got, err := File{Path: path(t)}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("messages = %d, want none", len(got))
	}
}

// TestLoadDropsATruncatedFinalLine covers a process that died mid-write.
func TestLoadDropsATruncatedFinalLine(t *testing.T) {
	t.Parallel()
	file := File{Path: path(t)}
	if err := file.Append(fantasy.NewUserMessage("kept")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	raw, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	truncated := string(raw) + `{"role":"assistant","content":[`
	if err = os.WriteFile(file.Path, []byte(truncated), fileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("messages = %d, want the two good lines", len(got))
	}
}

func TestLoadRejectsDamageInTheMiddle(t *testing.T) {
	t.Parallel()
	file := File{Path: path(t)}
	body := "{\"role\":\"user\",\"content\":[]}\nnot json\n{\"role\":\"user\",\"content\":[]}\n"
	if err := os.WriteFile(file.Path, []byte(body), fileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := file.Load()
	if err == nil {
		t.Fatal("Load accepted a damaged conversation")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("Load error = %q, want the line number", err)
	}
}

func TestResetDiscardsTheConversation(t *testing.T) {
	t.Parallel()
	file := File{Path: path(t)}
	if err := file.Append(fantasy.NewUserMessage("gone")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := file.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	got, err := file.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("messages = %d, want none", len(got))
	}
	if err = file.Reset(); err != nil {
		t.Fatalf("Reset of an absent conversation: %v", err)
	}
}

func TestAppendCreatesTheDirectory(t *testing.T) {
	t.Parallel()
	file := File{Path: filepath.Join(t.TempDir(), "profiles", "general", "conversation.jsonl")}
	if err := file.Append(fantasy.NewUserMessage("x")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	info, err := os.Stat(file.Path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Errorf("file mode = %o, want %o", perm, fileMode)
	}
	parent, err := os.Stat(filepath.Dir(file.Path))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := parent.Mode().Perm(); perm != dirMode {
		t.Errorf("directory mode = %o, want %o", perm, dirMode)
	}
}

func TestAppendWithNoMessages(t *testing.T) {
	t.Parallel()
	file := File{Path: path(t)}
	if err := file.Append(); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
		t.Fatalf("Append created a file for no messages: %v", err)
	}
}
