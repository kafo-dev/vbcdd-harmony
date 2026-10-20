package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const declaration = `
description = "Run a shell command."
args = { cmd = "string", timeout = "int?" }
target = "bash"
`

func mustParse(t *testing.T, body string) Spec {
	t.Helper()
	spec, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return spec
}

// script writes an executable shell script into a temporary directory and
// returns its path, which doubles as a command name.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

// run runs a target, retrying once on ETXTBSY. Linux refuses to execute a file
// that any process still holds open for writing, and under a loaded parallel
// test run a child forked by another test can inherit the descriptor of a
// script this one has just written.
func run(t *testing.T, runner Runner, spec Spec, args map[string]any) (string, error) {
	t.Helper()
	out, err := runner.Run(context.Background(), spec, args)
	if err != nil && errors.Is(err, syscall.ETXTBSY) {
		return runner.Run(context.Background(), spec, args)
	}
	return out, err
}

func TestParseReadsTheDeclaration(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, declaration)
	if spec.Target != "bash" {
		t.Errorf("target = %q", spec.Target)
	}
	args, err := spec.Arguments()
	if err != nil {
		t.Fatalf("Arguments: %v", err)
	}
	if len(args) != 2 {
		t.Fatalf("Arguments = %d, want 2", len(args))
	}
	if args[0].Name != "cmd" || args[0].Type != String || args[0].Optional {
		t.Errorf("first argument = %+v, want a required string", args[0])
	}
	if args[1].Name != "timeout" || args[1].Type != Int || !args[1].Optional {
		t.Errorf("second argument = %+v, want an optional int", args[1])
	}
}

func TestParseRejectsBadDeclarations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no target", "description = \"d\"\n", "target is empty"},
		{"no description", "target = \"t\"\n", "description is empty"},
		{"unknown type", "description = \"d\"\ntarget = \"t\"\nargs = { cmd = \"int3\" }\n", "unknown type"},
		{"invalid TOML", "description = ", "unexpected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.body))
			if err == nil {
				t.Fatal("Parse accepted the declaration")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsACall(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, declaration)
	args, err := spec.Validate(json.RawMessage(`{"cmd":"ls","timeout":5}`))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if args["cmd"] != "ls" || args["timeout"] != json.Number("5") {
		t.Fatalf("args = %#v, want the call's own values", args)
	}
}

func TestValidateAllowsAnOmittedOptionalArgument(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, declaration)
	args, err := spec.Validate(json.RawMessage(`{"cmd":"ls"}`))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(args) != 1 {
		t.Fatalf("args = %#v, want only cmd", args)
	}
}

func TestValidateAllowsAnEmptyCall(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, "description = \"d\"\ntarget = \"t\"\n")
	args, err := spec.Validate(nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none", args)
	}
}

func TestValidateRejectsBadCalls(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, declaration)
	cases := []struct {
		name string
		call string
		want string
	}{
		{"missing required", `{"timeout":5}`, `"cmd" is required`},
		{"wrong type", `{"cmd":5}`, `"cmd" must be of type string`},
		{"not an integer", `{"cmd":"ls","timeout":1.5}`, `"timeout" must be of type int`},
		{"undeclared", `{"cmd":"ls","extra":true}`, `"extra" is not declared`},
		{"not an object", `["ls"]`, "not a JSON object"},
		{"not JSON", `{`, "not a JSON object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.Validate(json.RawMessage(tc.call))
			if err == nil {
				t.Fatal("Validate accepted the call")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestRunFeedsTheArgumentsOnStdin(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, "description = \"d\"\ntarget = \""+script(t, "cat")+"\"\n")
	got, err := run(t, Runner{}, spec, map[string]any{"cmd": "ls"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(got) != `{"cmd":"ls"}` {
		t.Fatalf("stdout = %q, want the arguments", got)
	}
}

func TestRunExportsTheArguments(t *testing.T) {
	t.Parallel()
	body := `printf '%s|%s' "$VBCDD_ARG_CMD" "$VBCDD_ARG_DRY_RUN"`
	spec := mustParse(t, "description = \"d\"\ntarget = \""+script(t, body)+"\"\n")
	got, err := run(t, Runner{}, spec, map[string]any{"cmd": "ls", "dry-run": true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "ls|true" {
		t.Fatalf("stdout = %q, want %q", got, "ls|true")
	}
}

func TestRunReturnsTheOutputOfAFailingTarget(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, "description = \"d\"\ntarget = \""+script(t, "echo out; exit 3")+"\"\n")
	var logged bytes.Buffer
	runner := Runner{Log: slog.New(slog.NewTextHandler(&logged, nil))}
	got, err := run(t, runner, spec, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(got) != "out" {
		t.Fatalf("stdout = %q, want the output", got)
	}
	if !strings.Contains(logged.String(), "exited 3") {
		t.Fatalf("log = %q, want the exit status", logged.String())
	}
}

func TestRunReportsATimeout(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, "description = \"d\"\ntarget = \""+script(t, "sleep 5")+"\"\n")
	runner := Runner{Timeout: 50 * time.Millisecond}
	if _, err := run(t, runner, spec, nil); err == nil {
		t.Fatal("Run accepted a target that never finished")
	}
}

func TestRunReportsAMissingTarget(t *testing.T) {
	t.Parallel()
	spec := mustParse(t, "description = \"d\"\ntarget = \"no-such-command-anywhere\"\n")
	if _, err := run(t, Runner{}, spec, nil); err == nil {
		t.Fatal("Run accepted a target that does not exist")
	}
}

func TestEnvName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"cmd":     "CMD",
		"dry-run": "DRY_RUN",
		"maxSize": "MAXSIZE",
		"a.b":     "A_B",
	}
	for name, want := range cases {
		if got := envName(name); got != want {
			t.Errorf("envName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEnvValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value any
		want  string
	}{
		{"ls", "ls"},
		{true, "true"},
		{false, "false"},
		{json.Number("5"), "5"},
		{nil, ""},
		{[]string{"a"}, fmt.Sprint([]string{"a"})},
	}
	for _, tc := range cases {
		got := envValue(tc.value)
		if got != tc.want {
			t.Errorf("envValue(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
