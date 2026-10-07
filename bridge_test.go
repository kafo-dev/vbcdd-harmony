package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithAttachment(t *testing.T) {
	t.Parallel()
	got := withAttachment("body", "/state/media/1-pic.png", "image/png")
	if !strings.Contains(got, "/state/media/1-pic.png") || !strings.Contains(got, "image/png") ||
		!strings.HasSuffix(got, "body") {
		t.Fatalf("withAttachment = %q", got)
	}
	if empty := withAttachment("", "/state/media/1-pic.png", ""); strings.Contains(empty, "\n\n") {
		t.Errorf("empty body = %q", empty)
	}
}

func TestJoinBatch(t *testing.T) {
	t.Parallel()
	if got := joinBatch([]string{"only"}); got != "only" {
		t.Errorf("single = %q", got)
	}
	got := joinBatch([]string{"first", "second"})
	if !strings.Contains(got, "[message 1]") || !strings.Contains(got, "[message 2]") {
		t.Fatalf("joinBatch = %q", got)
	}
	if strings.Index(got, "first") > strings.Index(got, "second") {
		t.Errorf("order lost: %q", got)
	}
}

func TestFlushOutbox(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	outbox := filepath.Join(state, "outbox")
	if err := os.MkdirAll(outbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outbox, "big.bin"), make([]byte, maxOutboxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("Filename"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := &Bridge{
		cfg:  &Config{StateDir: state, DisplayName: "vbcdd-harmony"},
		ntfy: &Ntfy{baseURL: srv.URL, topic: "t", token: "tok", client: srv.Client()},
	}
	b.flushOutbox(context.Background())

	if len(sent) != 1 || sent[0] != "note.txt" {
		t.Fatalf("sent = %v", sent)
	}
	if _, err := os.Stat(filepath.Join(outbox, "note.txt")); !os.IsNotExist(err) {
		t.Errorf("note.txt not removed")
	}
	if _, err := os.Stat(filepath.Join(outbox, "big.bin.failed")); err != nil {
		t.Errorf("big.bin.failed missing: %v", err)
	}
}
