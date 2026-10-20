package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vbcdd.kafo.dev/harmony/internal/config"
)

// testConfig is the smallest configuration the package accepts.
func testConfig() config.Config {
	return config.Config{
		BaseURL:  "https://endpoint.example.com/v1",
		APIKey:   "sk-test",
		Model:    "example-model",
		MaxTurns: 20,
	}
}

func TestNewModelBuildsAModel(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	cfg := testConfig()
	cfg.BaseURL = server.URL
	if _, err := NewModel(context.Background(), cfg); err != nil {
		t.Fatalf("NewModel: %v", err)
	}
}

func TestNewModelRunsTheKeyCommand(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.APIKey = ""
	cfg.APIKeyCommand = "printf 'from-the-command\\n'"
	if _, err := NewModel(context.Background(), cfg); err != nil {
		t.Fatalf("NewModel: %v", err)
	}
}

func TestNewModelReportsABadKeyCommand(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"fails":          "exit 1",
		"prints nothing": "true",
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig()
			cfg.APIKey = ""
			cfg.APIKeyCommand = command
			_, err := NewModel(context.Background(), cfg)
			if err == nil {
				t.Fatal("NewModel accepted the command")
			}
			if !strings.Contains(err.Error(), "api_key_command") {
				t.Fatalf("error = %q, want the command named", err)
			}
		})
	}
}

func TestOptions(t *testing.T) {
	t.Parallel()
	if got := Options(testConfig()); got != nil {
		t.Fatalf("Options = %v, want none for an unset effort", got)
	}
	cfg := testConfig()
	cfg.ReasoningEffort = "low"
	if got := Options(cfg); got == nil {
		t.Fatal("Options = nil, want the configured effort")
	}
}
