// Command tgfake serves the tgfake server - a fake Telegram Bot API with a
// chat page and a simulation API - and optionally a scripted OpenAI-compatible
// model, so a bot runs with no token, no phone and no network. See README.md.
//
//	tgfake --llm                                  # fake Bot API + scripted model on :18790
//	export TELEGRAM_API=http://127.0.0.1:18790    # point the bot's Bot API origin here
//	open http://127.0.0.1:18790/                  # the person's side of the chat
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/EvilFreelancer/tgfake/pkg/server"
)

// version is set at link time by the release build (-X main.version=...).
var version = ""

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	switch {
	case errors.Is(err, flag.ErrHelp):
	case err != nil:
		fmt.Fprintln(os.Stderr, "tgfake:", err)
		os.Exit(1)
	}
}

// run parses args, serves the stand until ctx ends and returns nil on a clean
// shutdown. The banner goes to stdout, flag errors and usage to stderr.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tgfake", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:18790", "address to listen on; port 0 picks a free one")
	token := fs.String("token", "", "the only bot token accepted; empty accepts any")
	botUsername := fs.String("bot-username", server.DefaultBotUsername, "username getMe reports")
	botName := fs.String("bot-name", server.DefaultBotFirstName, "the bot's first name, as getMe and its messages report it")
	pollMax := fs.Duration("poll-max", 30*time.Second, "longest a getUpdates request is held open")
	verbose := fs.Bool("verbose", false, "print every Bot API call")
	showVersion := fs.Bool("version", false, "print the version and exit")

	llm := fs.Bool("llm", false, "also serve a scripted OpenAI-compatible model under /v1")
	llmModel := fs.String("llm-model", llmstub.DefaultModel, "model id the scripted model reports")
	llmScript := fs.String("llm-script", "", "JSON file with [{\"match\": \"...\", \"answer\": \"...\"}] rules; a rule with \"tool\": {\"name\": ..., \"arguments\": {...}} calls that tool first and answers its result")
	llmDelay := fs.Duration("llm-delay", 50*time.Millisecond, "pause between streamed chunks")
	llmChunk := fs.Int("llm-chunk-words", 1, "words per streamed chunk")
	var llmAnswers, llmStripTags stringList
	fs.Var(&llmAnswers, "llm-answer", "a canned answer, used in turn; repeatable")
	fs.Var(&llmStripTags, "llm-strip-tag", "a tag whose <tag>...</tag> blocks the client appends to user messages and the model ignores; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q: tgfake takes flags only", fs.Arg(0))
	}
	if !*llm {
		var orphan string
		fs.Visit(func(f *flag.Flag) {
			if orphan == "" && strings.HasPrefix(f.Name, "llm-") {
				orphan = f.Name
			}
		})
		if orphan != "" {
			return fmt.Errorf("--%s configures the scripted model, which only --llm serves", orphan)
		}
	}
	if *showVersion {
		_, err := fmt.Fprintf(stdout, "tgfake %s\n", currentVersion())
		return err
	}

	opts := server.Options{Token: *token, BotUsername: *botUsername, BotFirstName: *botName, MaxPollWait: *pollMax}
	if *verbose {
		opts.Logf = log.New(stderr, "", log.LstdFlags).Printf
	}
	var stub *llmstub.Server
	if *llm {
		stub = &llmstub.Server{Model: *llmModel, Answers: llmAnswers, Delay: *llmDelay, ChunkWords: *llmChunk, StripTags: llmStripTags}
		if *llmScript != "" {
			rules, err := loadRules(*llmScript)
			if err != nil {
				return err
			}
			stub.Rules = rules
		}
	}

	fake := server.New(opts)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: newMux(fake, stub), ReadHeaderTimeout: 10 * time.Second}
	printBanner(stdout, "http://"+ln.Addr().String(), *botUsername, stub)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		fake.Close()
		return err
	case <-ctx.Done():
	}
	// Release the long polls first, then let the listener drain.
	fake.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// currentVersion is the release version linked in, else the module version
// `go install ...@vX` records, else "dev".
func currentVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// newMux mounts the fake and, when given, the scripted model.
func newMux(fake *server.Server, stub *llmstub.Server) http.Handler {
	if stub == nil {
		return fake.Handler()
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", stub.Handler())
	mux.Handle("/", fake.Handler())
	return mux
}

func printBanner(w io.Writer, origin, botUsername string, stub *llmstub.Server) {
	_, _ = fmt.Fprintf(w, "tgfake %s: fake Bot API for @%s at %s\n", currentVersion(), botUsername, origin)
	_, _ = fmt.Fprintf(w, "  Bot API:    %s/bot<token>/<method>  (in place of https://api.telegram.org)\n", origin)
	_, _ = fmt.Fprintf(w, "  chat page:  %s/\n", origin)
	_, _ = fmt.Fprintf(w, "  sim API:    %s/sim/\n", origin)
	if stub != nil {
		model := stub.Model
		if model == "" {
			model = llmstub.DefaultModel
		}
		_, _ = fmt.Fprintf(w, "  model:      %s/v1  (OpenAI-compatible, model %s, any API key)\n", origin, model)
	}
}

func loadRules(path string) ([]llmstub.Rule, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, err
	}
	var rules []llmstub.Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ", ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }
