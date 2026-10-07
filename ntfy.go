package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// markerTag marks an outbound message so the subscription drops its own echo.
// One topic carries both directions, so the bot reads its own publishes.
const markerTag = "vbcdd-harmony"

// maxBodyBytes is the ntfy message size cap. A longer body would become an
// attachment, so the client splits it at line boundaries instead.
const maxBodyBytes = 4096

// maxMediaBytes caps one downloaded attachment, so a runaway upload cannot
// fill the disk.
const maxMediaBytes = 64 << 20

// maxOutboxBytes caps one outbound attachment. A larger file is marked failed
// rather than sent, because the ntfy server rejects it.
const maxOutboxBytes = 15 << 20

// sendTimeout bounds one publish. The subscription client carries no timeout,
// because a stream stays open between messages.
const sendTimeout = 30 * time.Second

const (
	// drainBytes bounds the response body a publish reads before it checks
	// the status.
	drainBytes = 4096

	// scanStart and scanMax bound one line of a streaming response.
	scanStart = 64 << 10
	scanMax   = 1 << 20

	// inboxBuffer sizes the queue between the subscription and the agent.
	inboxBuffer = 64
)

// Message is one outbound notification.
type Message struct {
	Title    string
	Body     string
	Priority int
	Tags     []string
}

// Ntfy is the transport: one topic, published to and subscribed from.
type Ntfy struct {
	baseURL string
	topic   string
	token   string
	client  *http.Client
	inbox   chan Incoming
}

// Incoming is one owner message taken from the topic.
type Incoming struct {
	ID         string
	Time       time.Time
	Body       string
	Attachment *Attachment
}

// Attachment is a media item carried by an owner message.
type Attachment struct {
	URL      string
	Name     string
	MimeType string
	Size     int64
}

// NewNtfy builds the client.
func NewNtfy(cfg *Config) *Ntfy {
	return &Ntfy{
		baseURL: strings.TrimRight(cfg.NtfyURL, "/"),
		topic:   cfg.NtfyTopic,
		token:   cfg.NtfyToken,
		client:  &http.Client{},
		inbox:   make(chan Incoming, inboxBuffer),
	}
}

// Messages is the stream of owner messages.
func (n *Ntfy) Messages() chan Incoming {
	return n.inbox
}

// Send publishes one message. A body over `maxBodyBytes` is split at line
// boundaries and published as consecutive messages.
func (n *Ntfy) Send(ctx context.Context, msg Message) error {
	for _, chunk := range splitMessage(msg.Body, maxBodyBytes) {
		if err := n.publish(ctx, msg, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (n *Ntfy) publish(ctx context.Context, msg Message, body string) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	endpoint := n.baseURL + "/" + url.PathEscape(n.topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Markdown", "yes")
	req.Header.Set("Authorization", "Bearer "+n.token)
	if msg.Title != "" {
		req.Header.Set("Title", msg.Title)
	}
	if msg.Priority != 0 {
		req.Header.Set("Priority", strconv.Itoa(msg.Priority))
	}
	req.Header.Set("Tags", strings.Join(append([]string{markerTag}, msg.Tags...), ","))

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	defer resp.Body.Close()
	if _, err = io.Copy(io.Discard, io.LimitReader(resp.Body, drainBytes)); err != nil {
		return fmt.Errorf("publish: drain: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("publish: %s", resp.Status)
	}
	return nil
}

// SendFile publishes one local file as an ntfy attachment. The ntfy API takes
// the upload in the request body, with the name in the `Filename` header.
func (n *Ntfy) SendFile(ctx context.Context, path, name, title string) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	endpoint := n.baseURL + "/" + url.PathEscape(n.topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, file)
	if err != nil {
		return err
	}
	req.Header.Set("Filename", name)
	req.Header.Set("Authorization", "Bearer "+n.token)
	req.Header.Set("Tags", markerTag)
	if title != "" {
		req.Header.Set("Title", title)
	}
	if info, statErr := file.Stat(); statErr == nil {
		req.ContentLength = info.Size()
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("publish file: %w", err)
	}
	defer resp.Body.Close()
	if _, err = io.Copy(io.Discard, io.LimitReader(resp.Body, drainBytes)); err != nil {
		return fmt.Errorf("publish file: drain: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("publish file: %s", resp.Status)
	}
	return nil
}

// Subscribe streams the topic, handing owner messages to the inbox. The loop
// reconnects after a dropped stream, so a server restart does not end delivery.
// Outbound messages carry `markerTag`; the subscription drops them.
func (n *Ntfy) Subscribe(ctx context.Context, logf func(string, ...any)) error {
	for ctx.Err() == nil {
		if err := n.subscribeOnce(ctx); err != nil && ctx.Err() == nil {
			logf("subscribe: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return ctx.Err()
}

func (n *Ntfy) subscribeOnce(ctx context.Context) error {
	endpoint := n.baseURL + "/" + url.PathEscape(n.topic) + "/json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("subscribe: %s", resp.Status)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, scanStart), scanMax)
	for sc.Scan() {
		in, ok := parseMessage(sc.Bytes())
		if !ok {
			continue
		}
		select {
		case n.inbox <- in:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sc.Err()
}

// ntfyEvent is one line of the streaming JSON response. Only the `message`
// event carries a body.
type ntfyEvent struct {
	ID         string   `json:"id"`
	Time       int64    `json:"time"`
	Event      string   `json:"event"`
	Tags       []string `json:"tags"`
	Message    string   `json:"message"`
	Attachment *struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"attachment"`
}

// parseMessage turns one stream line into an Incoming. It reports false for a
// non-message event, the bot's own echo, and an empty message.
func parseMessage(line []byte) (Incoming, bool) {
	var ev ntfyEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return Incoming{}, false
	}
	if ev.Event != "message" {
		return Incoming{}, false
	}
	if slices.Contains(ev.Tags, markerTag) {
		return Incoming{}, false
	}
	in := Incoming{ID: ev.ID, Body: ev.Message}
	if ev.Time != 0 {
		in.Time = time.Unix(ev.Time, 0)
	}
	if ev.Attachment != nil {
		in.Attachment = &Attachment{
			URL:      ev.Attachment.URL,
			Name:     ev.Attachment.Name,
			MimeType: ev.Attachment.Type,
			Size:     ev.Attachment.Size,
		}
	}
	if strings.TrimSpace(in.Body) == "" && in.Attachment == nil {
		return Incoming{}, false
	}
	return in, true
}

// Download fetches one attachment and writes it under dir. The ntfy attachment
// URL expires a few hours after the upload, so a late download fails.
func (n *Ntfy) Download(ctx context.Context, dir string, att Attachment) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, att.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := n.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaBytes+1))
	if err != nil {
		return "", fmt.Errorf("read media: %w", err)
	}
	if len(data) > maxMediaBytes {
		return "", fmt.Errorf("media exceeds %d bytes", maxMediaBytes)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, "*-"+sanitizeBase(att.Name))
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return "", err
	}
	return file.Name(), nil
}

// splitMessage breaks a body into chunks of at most limit bytes, splitting at
// line boundaries. A single line longer than limit stays whole; the server
// converts that one to an attachment.
func splitMessage(body string, limit int) []string {
	if len(body) <= limit {
		return []string{body}
	}
	var chunks []string
	var b strings.Builder
	for _, line := range strings.SplitAfter(body, "\n") {
		if b.Len() > 0 && b.Len()+len(line) > limit {
			chunks = append(chunks, b.String())
			b.Reset()
		}
		b.WriteString(line)
	}
	if b.Len() > 0 {
		chunks = append(chunks, b.String())
	}
	return chunks
}

// sanitizeBase reduces a file name to a flat, safe base name for the media
// directory, keeping the extension the reader uses to detect the type.
func sanitizeBase(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		return "attachment"
	}
	return out
}
