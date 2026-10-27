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
	"github.com/EvilFreelancer/tgfake/pkg/serverless"
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
	inlineMode := fs.Bool("inline", false, "enable the bot's inline mode, as in BotFather")
	allowHTTPWebhook := fs.Bool("allow-http-webhook", false, "allow non-loopback HTTP webhooks on any port for container tests")
	showVersion := fs.Bool("version", false, "print the version and exit")
	project := fs.String("serverless", "", "local project containing tgcloud/; run JavaScript in Docker")
	dockerImage := fs.String("serverless-image", serverless.DefaultDockerImage, "Docker image providing Node.js and node:sqlite; pull it first")
	database := fs.String("serverless-db", "", "SQLite file; empty uses a temporary database")
	migrate := fs.Bool("serverless-migrate", false, "explicitly create serverless schema tables and indexes at startup")
	invocationTimeout := fs.Duration("serverless-timeout", 10*time.Second, "maximum duration of a serverless invocation, including container startup")

	llm := fs.Bool("llm", false, "also serve a scripted OpenAI-compatible model under /v1")
	llmModel := fs.String("llm-model", llmstub.DefaultModel, "model id the scripted model reports")
	llmScript := fs.String("llm-script", "", "JSON file with [{\"match\": \"...\", \"answer\": \"...\"}] rules; a rule with \"tool\": {\"name\": ..., \"arguments\": {...}} calls that tool first and answers its result")
	llmDelay := fs.Duration("llm-delay", 50*time.Millisecond, "pause between streamed chunks")
	llmClientHeader := fs.String("llm-client-header", "", "HTTP header selecting an LLM scenario client instead of request.user")
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
	if *project == "" {
		var orphan string
		fs.Visit(func(f *flag.Flag) {
			if orphan == "" && strings.HasPrefix(f.Name, "serverless-") {
				orphan = f.Name
			}
		})
		if orphan != "" {
			return fmt.Errorf("--%s requires --serverless", orphan)
		}
	} else if *token == "" {
		*token = "123456:SERVERLESS"
	}

	opts := server.Options{Token: *token, BotUsername: *botUsername, BotFirstName: *botName, MaxPollWait: *pollMax, InlineMode: *inlineMode, AllowHTTPWebhook: *allowHTTPWebhook}
	if *verbose {
		opts.Logf = log.New(stderr, "", log.LstdFlags).Printf
	}
	var stub *llmstub.Server
	if *llm {
		if err := llmstub.CheckClientHeader(*llmClientHeader); err != nil {
			return fmt.Errorf("--llm-client-header: %w", err)
		}
		stub = &llmstub.Server{ClientHeader: *llmClientHeader, Model: *llmModel, Answers: llmAnswers, Delay: *llmDelay, ChunkWords: *llmChunk, StripTags: llmStripTags}
		if *llmScript != "" {
			rules, err := loadRules(*llmScript)
			if err != nil {
				return err
			}
			if err := stub.SetRules(rules); err != nil {
				return err
			}
		}
	}

	fake := server.New(opts)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	origin := "http://" + ln.Addr().String()
	apiOrigin := strings.ReplaceAll(strings.ReplaceAll(origin, "//0.0.0.0:", "//127.0.0.1:"), "//[::]:", "//127.0.0.1:")
	handler := newMux(fake, stub)
	var cloud *serverless.Runtime
	if *project != "" {
		cloud, err = serverless.New(serverless.Options{
			ProjectDir: *project, APIURL: apiOrigin, Token: *token,
			DockerImage: *dockerImage, DatabasePath: *database, Timeout: *invocationTimeout,
			Logf: log.New(stderr, "", log.LstdFlags).Printf,
		})
		if err != nil {
			fake.Close()
			return err
		}
		defer cloud.Close()
		if *migrate {
			if err := cloud.Migrate(ctx); err != nil {
				fake.Close()
				return err
			}
		}
		mux := http.NewServeMux()
		mux.Handle("/api/", cloud.Handler())
		mux.Handle("/", handler)
		handler = mux
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	printBanner(stdout, "http://"+ln.Addr().String(), *botUsername, stub)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	pollCtx, stopPoll := context.WithCancel(ctx)
	defer stopPoll()
	polled := make(chan error, 1)
	if cloud != nil {
		go func() {
			polled <- cloud.Poll(pollCtx)
			close(polled)
		}()
	}
	// waitPoll waits for the poller to stop. Its error after a shutdown is
	// expected, and one that came first is already in pollErr; ranging to the
	// close also returns when that one was taken, where a second receive
	// would wait forever.
	waitPoll := func() {
		for range polled {
		}
	}
	var pollErr error
	select {
	case err := <-served:
		stopPoll()
		fake.Close()
		if cloud != nil {
			cloud.Close()
			waitPoll()
		}
		return err
	case pollErr = <-polled:
	case <-ctx.Done():
	}
	stopPoll()
	if cloud != nil {
		cloud.Close()
		waitPoll()
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
	if pollErr != nil && ctx.Err() == nil {
		return pollErr
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
	model, telegram := stub.Handler(), fake.Handler()
	mux := http.NewServeMux()
	mux.Handle("/v1/", model)
	mux.Handle("/sim/llm/", model)
	// One reset for the stand: the model's cursors and journal, then the fake.
	mux.HandleFunc("POST /sim/reset", func(w http.ResponseWriter, r *http.Request) { stub.Reset(); telegram.ServeHTTP(w, r) })
	mux.Handle("/", telegram)
	return mux
}

// printBanner writes the banner in one write, so a reader that waits for its
// first line never sees half of it.
func printBanner(w io.Writer, origin, botUsername string, stub *llmstub.Server) {
	var b strings.Builder
	fmt.Fprintf(&b, "tgfake %s: fake Bot API for @%s at %s\n", currentVersion(), botUsername, origin)
	fmt.Fprintf(&b, "  Bot API:    %s/bot<token>/<method>  (in place of https://api.telegram.org)\n", origin)
	fmt.Fprintf(&b, "  chat page:  %s/\n", origin)
	fmt.Fprintf(&b, "  sim API:    %s/sim/\n", origin)
	if stub != nil {
		model := stub.Model
		if model == "" {
			model = llmstub.DefaultModel
		}
		fmt.Fprintf(&b, "  model:      %s/v1  (OpenAI-compatible, model %s, any API key)\n", origin, model)
	}
	_, _ = io.WriteString(w, b.String())
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
