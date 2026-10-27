package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/EvilFreelancer/tgfake/pkg/server"
)

func TestNewMux_ServesFakeAndModel(t *testing.T) {
	fake := server.New(server.Options{MaxPollWait: 100 * time.Millisecond})
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

func TestLLMDiagnosticsScriptAndCombinedReset(t *testing.T) {
	c := startCommand(t, "--llm", "--llm-script", "../../examples/dialogbot/script.json", "--llm-client-header", "X-Test-Client")
	invoke := func() string {
		t.Helper()
		req, err := http.NewRequest("POST", c.origin+"/v1/chat/completions", strings.NewReader(`{"model":"agent","messages":[{"role":"user","content":"same"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Test-Client", "alice")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode, string(raw))
		}
		return string(raw)
	}
	if body := invoke(); !strings.Contains(body, "Which style?") {
		t.Fatal(body)
	}
	post := func(path string) map[string]any {
		t.Helper()
		res, err := http.Post(c.origin+path, "application/json", strings.NewReader(`{"text":"hi"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var body map[string]any
		if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	first := post("/sim/message")["update_id"].(float64)
	post("/sim/reset")
	second := post("/sim/message")["update_id"].(float64)
	if second <= first {
		t.Fatal("combined reset rewound Telegram update IDs")
	}
	if body := invoke(); !strings.Contains(body, "Which style?") {
		t.Fatal("combined reset did not rewind LLM", body)
	}
	res, err := http.Get(c.origin + "/sim/llm/log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var log struct {
		Count   int
		Entries []llmstub.Entry
	}
	if err = json.NewDecoder(res.Body).Decode(&log); err != nil {
		t.Fatal(err)
	}
	if log.Count != 1 || log.Entries[0].Client != "alice" || log.Entries[0].Seq < 2 {
		t.Fatal(log)
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

// A model flag without --llm would be ignored, a missing script included; the
// command says so instead of serving a stand without the model asked for.
func TestRunRefusesModelFlagsWithoutLLM(t *testing.T) {
	for _, args := range [][]string{
		{"--llm-script", "missing.json"},
		{"--llm-answer", "hi"},
		{"--llm-strip-tag", "turn_context"},
		{"--llm-model", "x"},
	} {
		err := run(context.Background(), append([]string{"--addr", "127.0.0.1:0"}, args...), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "--llm") {
			t.Errorf("%v without --llm: %v", args, err)
		}
	}
}

// The flag parser stops at the first word that is not a flag, so anything
// after one would be dropped unseen.
func TestRunRefusesAPositionalArgument(t *testing.T) {
	err := run(context.Background(), []string{"--addr", "127.0.0.1:0", "extra", "--llm"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("a positional argument was accepted: %v", err)
	}
}

func TestAllowHTTPWebhookFlag(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		accepted bool
	}{
		{"strict default", nil, false},
		{"explicit opt-in", []string{"--allow-http-webhook"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := startCommand(t, tc.args...)
			body := c.botCall("setWebhook", url.Values{"url": {"http://bot:8080/webhook"}})
			if body["ok"] != tc.accepted {
				t.Fatalf("setWebhook: %v", body)
			}
		})
	}
}
