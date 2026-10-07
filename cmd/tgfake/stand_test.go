package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is the command's stdout, read by the test while run writes it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// command is one run of the command in-process, on a port of its own.
type command struct {
	t      *testing.T
	origin string
	out    *syncBuffer
	cancel context.CancelFunc
	done   chan error
}

var originRe = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)

// startCommand runs the command with args on a free loopback port and waits
// for the banner to name its origin. The run is stopped by the test's cleanup.
func startCommand(t *testing.T, args ...string) *command {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &command{t: t, out: &syncBuffer{}, cancel: cancel, done: make(chan error, 1)}
	args = append([]string{"--addr", "127.0.0.1:0", "--poll-max", "200ms"}, args...)
	go func() { c.done <- run(ctx, args, c.out, io.Discard) }()
	t.Cleanup(func() { _ = c.stop() })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if origin := originRe.FindString(c.out.String()); origin != "" {
			c.origin = origin
			return c
		}
		select {
		case err := <-c.done:
			t.Fatalf("the command exited before serving: %v\n%s", err, c.out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("no origin in the banner:\n%s", c.out.String())
	return nil
}

// stop cancels the run and returns what run returned.
func (c *command) stop() error {
	c.cancel()
	select {
	case err := <-c.done:
		c.done <- err
		return err
	case <-time.After(5 * time.Second):
		c.t.Fatalf("the command did not stop within 5 s")
		return nil
	}
}

// botCall posts a Bot API method the way a bot library does.
func (c *command) botCall(method string, form url.Values) map[string]any {
	c.t.Helper()
	resp, err := http.Post(c.origin+"/bot123456:TOKEN/"+method, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	return decode(c.t, resp)
}

// postJSON posts a JSON body to a path of the stand.
func (c *command) postJSON(path string, body any) map[string]any {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(c.origin+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	return decode(c.t, resp)
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %d %q is not JSON: %v", resp.Request.URL.Path, resp.StatusCode, raw, err)
	}
	return out
}

// completion asks the scripted model for one blocking answer to messages.
func (c *command) completion(messages ...map[string]any) string {
	c.t.Helper()
	body := c.postJSON("/v1/chat/completions", map[string]any{"model": "x", "messages": messages})
	choices, _ := body["choices"].([]any)
	if len(choices) != 1 {
		c.t.Fatalf("completion: %v", body)
	}
	msg, _ := choices[0].(map[string]any)["message"].(map[string]any)
	content, _ := msg["content"].(string)
	return content
}
