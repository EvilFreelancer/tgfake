package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandRunsServerlessProject(t *testing.T) {
	if err := exec.Command("docker", "image", "inspect", "node:24-bookworm-slim").Run(); err != nil {
		if os.Getenv("TGFAKE_TEST_DOCKER") == "1" {
			t.Fatalf("Docker integration is required: %v", err)
		}
		t.Skip("serverless integration requires Docker and node:24-bookworm-slim")
	}
	project, err := filepath.Abs("../../examples/serverless")
	if err != nil {
		t.Fatal(err)
	}
	c := startCommand(t, "--serverless", project, "--serverless-db", filepath.Join(t.TempDir(), "bot.sqlite"), "--serverless-migrate", "--inline")
	c.postJSON("/sim/message", map[string]any{"text": "hello"})
	waitCalls := func(method string, count int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(c.origin + "/sim/outbox?method=" + method)
			if err != nil {
				t.Fatal(err)
			}
			body := decode(t, resp)
			if calls, _ := body["calls"].([]any); len(calls) >= count {
				if last := calls[len(calls)-1].(map[string]any); last["status"] != float64(200) {
					t.Fatalf("%s failed: %v", method, last)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("serverless handler did not call %s", method)
	}
	waitCalls("sendMessage", 1)
	c.postJSON("/sim/media", map[string]any{"type": "document", "file_name": "hello.txt", "data": []byte("hello")})
	waitCalls("sendMessage", 2)
	resp, err := http.Get(c.origin + "/sim/chat/4242")
	if err != nil {
		t.Fatal(err)
	}
	chat := decode(t, resp)
	messages := chat["messages"].([]any)
	if messages[len(messages)-1].(map[string]any)["text"] != "Received 5 bytes." {
		t.Fatalf("document download reply: %v", chat)
	}
	query := c.postJSON("/sim/inline", map[string]any{"query": "inline echo"})
	waitCalls("answerInlineQuery", 1)
	c.postJSON("/sim/inline/choose", map[string]any{"inline_query_id": query["inline_query_id"], "result_id": "echo"})
}

func TestServerlessFlagsRequireAProject(t *testing.T) {
	for _, args := range [][]string{{"--serverless-db", "db.sqlite"}, {"--serverless-migrate"}, {"--serverless-timeout", "1s"}, {"--serverless-image", "node:24"}} {
		err := run(context.Background(), args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "requires --serverless") {
			t.Fatalf("orphan flag %v: %v", args, err)
		}
	}
}
