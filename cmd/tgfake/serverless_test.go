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
	c := startCommand(t, "--serverless", project, "--serverless-db", filepath.Join(t.TempDir(), "bot.sqlite"), "--serverless-migrate")
	c.postJSON("/sim/message", map[string]any{"text": "hello"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(c.origin + "/sim/outbox?method=sendMessage")
		if err != nil {
			t.Fatal(err)
		}
		body := decode(t, resp)
		if calls, _ := body["calls"].([]any); len(calls) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("serverless handler did not call sendMessage")
}

func TestServerlessFlagsRequireAProject(t *testing.T) {
	for _, args := range [][]string{{"--serverless-db", "db.sqlite"}, {"--serverless-migrate"}, {"--serverless-timeout", "1s"}, {"--serverless-image", "node:24"}} {
		err := run(context.Background(), args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "requires --serverless") {
			t.Fatalf("orphan flag %v: %v", args, err)
		}
	}
}
