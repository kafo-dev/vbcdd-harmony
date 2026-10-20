package config

import (
	"strings"
	"testing"
)

const goodConfig = `
display_name = "vbcdd-harmony"
ntfy_url = "https://ntfy.example.com"
ntfy_token = "tok"

[topics]
"vbcdd-general" = "general"
"vbcdd-harmony" = "scheduler"
`

func TestParseReadsTopics(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(goodConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.DisplayName != "vbcdd-harmony" {
		t.Errorf("display_name = %q", cfg.DisplayName)
	}
	if got, ok := cfg.Profile("vbcdd-harmony"); !ok || got != "scheduler" {
		t.Errorf("Profile = %q %v, want scheduler", got, ok)
	}
	if _, ok := cfg.Profile("vbcdd-other"); ok {
		t.Error("Profile found an unmapped topic")
	}
}

func TestParseDefaultsTheDisplayName(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte("ntfy_url = \"https://ntfy.example.com\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.DisplayName != defaultDisplayName {
		t.Errorf("display_name = %q, want %q", cfg.DisplayName, defaultDisplayName)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"invalid TOML", "ntfy_url = ", "unexpected"},
		{"empty url", "[topics]\n", "ntfy_url is empty"},
		{"bad topic name", "ntfy_url = \"u\"\n[topics]\n\"has space\" = \"general\"\n", "not a valid ntfy topic"},
		{"bad profile name", "ntfy_url = \"u\"\n[topics]\n\"topic\" = \"General\"\n", "not a valid profile name"},
		{"profile name with a dot", "ntfy_url = \"u\"\n[topics]\n\"topic\" = \"a.b\"\n", "not a valid profile name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.body))
			if err == nil {
				t.Fatal("Parse accepted the configuration")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(goodConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err = cfg.Validate([]string{"general", "scheduler"}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsUnknownProfile(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(goodConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = cfg.Validate([]string{"general"})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("Validate error = %v, want a complaint about the missing profile", err)
	}
}

func TestValidateRejectsUnroutedProfile(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(goodConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = cfg.Validate([]string{"general", "scheduler", "extra"})
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("Validate error = %v, want a complaint about the unrouted profile", err)
	}
}
