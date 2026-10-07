package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSendHeaders(t *testing.T) {
	t.Parallel()
	var (
		gotHeader http.Header
		gotBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
	}))
	defer srv.Close()

	n := &Ntfy{baseURL: srv.URL, topic: "vbcdd-harmony", token: "tok", client: srv.Client()}
	err := n.Send(context.Background(), Message{
		Title:    "vbcdd-harmony",
		Body:     "hello",
		Priority: 4,
		Tags:     []string{"alarm_clock"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody != "hello" {
		t.Errorf("body = %q", gotBody)
	}
	if got := gotHeader.Get("Markdown"); got != "yes" {
		t.Errorf("Markdown = %q", got)
	}
	if got := gotHeader.Get("Title"); got != "vbcdd-harmony" {
		t.Errorf("Title = %q", got)
	}
	if got := gotHeader.Get("Priority"); got != "4" {
		t.Errorf("Priority = %q", got)
	}
	if got := gotHeader.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
	if tags := gotHeader.Get("Tags"); !strings.Contains(tags, markerTag) || !strings.Contains(tags, "alarm_clock") {
		t.Errorf("Tags = %q", tags)
	}
}

func TestSubscribeSkipsAndYields(t *testing.T) {
	t.Parallel()
	lines := []string{
		`{"id":"1","event":"open","topic":"vbcdd-harmony"}`,
		`{"id":"2","event":"keepalive","topic":"vbcdd-harmony"}`,
		`{"id":"3","event":"message","topic":"vbcdd-harmony","tags":["` + markerTag + `"],"message":"self"}`,
		`{"id":"4","event":"message","topic":"vbcdd-harmony","message":"hello","time":1700000000}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("no flusher")
			return
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
		fl.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := &Ntfy{
		baseURL: srv.URL,
		topic:   "vbcdd-harmony",
		token:   "tok",
		client:  srv.Client(),
		inbox:   make(chan Incoming, 4),
	}
	go n.Subscribe(ctx, t.Logf)

	select {
	case in := <-n.inbox:
		if in.ID != "4" || in.Body != "hello" {
			t.Fatalf("incoming = %+v", in)
		}
		if in.Time.Unix() != 1700000000 {
			t.Errorf("time = %v", in.Time)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no incoming message")
	}
	cancel()
}

func TestSplitMessage(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("a", 2000) + "\n" + strings.Repeat("b", 2000) + "\n" + strings.Repeat("c", 2000)
	chunks := splitMessage(body, maxBodyBytes)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if strings.Join(chunks, "") != body {
		t.Error("split changed the body")
	}
	for _, chunk := range chunks {
		if len(chunk) > maxBodyBytes {
			t.Errorf("chunk is %d bytes", len(chunk))
		}
	}
	if got := splitMessage("short", maxBodyBytes); len(got) != 1 || got[0] != "short" {
		t.Errorf("short body = %q", got)
	}
}

func TestSanitizeBase(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"photo 1.png":      "photo_1.png",
		"../../etc/passwd": "passwd",
		"a/b/c.jpg":        "c.jpg",
		"":                 "attachment",
		"..":               "attachment",
	}
	for in, want := range cases {
		if got := sanitizeBase(in); got != want {
			t.Errorf("sanitizeBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSendFileHeaders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(path, []byte("pdf-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		gotMethod string
		gotHeader http.Header
		gotBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Clone()
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
	}))
	defer srv.Close()

	n := &Ntfy{baseURL: srv.URL, topic: "vbcdd-harmony", token: "tok", client: srv.Client()}
	if err := n.SendFile(context.Background(), path, "report.pdf", "vbcdd-harmony"); err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if got := gotHeader.Get("Filename"); got != "report.pdf" {
		t.Errorf("Filename = %q", got)
	}
	if got := gotHeader.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
	if tags := gotHeader.Get("Tags"); !strings.Contains(tags, markerTag) {
		t.Errorf("Tags = %q", tags)
	}
	if gotBody != "pdf-bytes" {
		t.Errorf("body = %q", gotBody)
	}
}
