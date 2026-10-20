package tool

import (
	"encoding/json"
	"strings"
	"testing"
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
