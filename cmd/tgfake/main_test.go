package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake"
	"github.com/EvilFreelancer/tgfake/llmstub"
)

func TestNewMux_ServesFakeAndModel(t *testing.T) {
	fake := tgfake.New(tgfake.Options{MaxPollWait: 100 * time.Millisecond})
	srv := httptest.NewServer(newMux(fake, &llmstub.Server{}))
	defer func() {
		fake.Close()
		srv.Close()
	}()
	for _, path := range []string{"/", "/bot1/getMe", "/v1/models"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	bare := httptest.NewServer(newMux(fake, nil))
	defer bare.Close()
	resp, err := http.Get(bare.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("without --llm, /v1/models = %d", resp.StatusCode)
	}
}

func TestRunVersionPrintsTheVersionAndServesNothing(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out, io.Discard); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got := out.String(); got != "tgfake "+currentVersion()+"\n" {
		t.Fatalf("--version printed %q", got)
	}
}

func TestCurrentVersionPrefersTheLinkedVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3"
	if got := currentVersion(); got != "v1.2.3" {
		t.Fatalf("currentVersion = %q, want the version set at link time", got)
	}
}

func TestRunRefusesAnUnknownFlag(t *testing.T) {
	var errOut bytes.Buffer
	if err := run(context.Background(), []string{"--no-such-flag"}, io.Discard, &errOut); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

func TestRunRefusesALLMScriptThatIsNotARuleList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(`{"match": "x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"--addr", "127.0.0.1:0", "--llm", "--llm-script", path}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("a script that is not a list was accepted: %v", err)
	}
}

// The banner tells any bot how to reach the stand; it names no particular
// client of it.
func TestBannerNamesTheStandAndNoParticularBot(t *testing.T) {
	c := startCommand(t, "--llm")
	banner := c.out.String()
	for _, want := range []string{
		"@tgfake_bot",
		c.origin + "/bot<token>/<method>",
		c.origin + "/sim/",
		c.origin + "/v1",
		"tgfake-demo",
	} {
		if !strings.Contains(banner, want) {
			t.Errorf("the banner lacks %q:\n%s", want, banner)
		}
	}
	if strings.Contains(strings.ToLower(banner), "coddy") {
		t.Errorf("the banner names a particular bot:\n%s", banner)
	}
}

func TestBotFlagsSetTheIdentityGetMeReports(t *testing.T) {
	c := startCommand(t, "--bot-username", "echo_bot", "--bot-name", "Echo Bot")
	result, _ := c.botCall("getMe", nil)["result"].(map[string]any)
	if result["username"] != "echo_bot" || result["first_name"] != "Echo Bot" {
		t.Fatalf("getMe = %v", result)
	}
}

// A client that appends machine-made context to every request (Coddy's
// <turn_context> block) names its tag, and the model answers the person.
func TestLLMStripTagFlagCutsAppendedContext(t *testing.T) {
	c := startCommand(t, "--llm", "--llm-delay", "0s", "--llm-strip-tag", "turn_context")
	got := c.completion(
		map[string]any{"role": "user", "content": "hello"},
		map[string]any{"role": "user", "content": "<turn_context>state</turn_context>"},
	)
	if got != "You said: hello" {
		t.Fatalf("answer = %q", got)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	c := startCommand(t)
	if err := c.stop(); err != nil {
		t.Fatalf("run returned %v on a cancelled context", err)
	}
}
