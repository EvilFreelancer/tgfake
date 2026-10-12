// Package serverless runs local Telegram Serverless projects against a Bot API
// origin. JavaScript and SQLite are provided by Node.js in a Docker container;
// Go callers and the ordinary tgfake server need only the standard library.
package serverless

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/webapp"
)

//go:embed db.mjs
var databaseSDK string

//go:embed sdk.mjs
var platformSDK string

//go:embed runner.mjs
var runner string

// Options configures a local project. ProjectDir contains the tgcloud directory.
type Options struct {
	ProjectDir   string
	APIURL       string        // Bot API origin, normally an httptest server or tgfake
	Token        string        // also verifies endpoint initData; required
	DockerPath   string        // defaults to docker on PATH
	DockerImage  string        // defaults to DefaultDockerImage; must provide Node.js 22.13+
	DatabasePath string        // empty uses a temporary database removed by Close
	Timeout      time.Duration // per invocation, defaults to 10 seconds
	Logf         func(format string, args ...any)
}

// Result contains a module's return value and captured console output.
type Result struct {
	Value json.RawMessage `json:"value"`
	Logs  []Log           `json:"logs"`
}

// Log is one console call, tagged with its level.
type Log struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// InvocationError is an exception thrown by a module. EndpointError parameters
// are kept separately so endpoints can report them without leaking other errors.
type InvocationError struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func (e *InvocationError) Error() string { return e.Description }

// Runtime is a snapshot of a project's modules with a persistent SQLite database.
// Calls are serialized; each invocation gets fresh module globals. The runtime
// accepts trusted local code, and is not a security boundary for hostile scripts.
type Runtime struct {
	opts    Options
	modules map[string]string
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	tempDir string
}

var endpointName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DefaultDockerImage supplies JavaScript and built-in SQLite without npm packages.
const DefaultDockerImage = "node:24-bookworm-slim"

// New loads a project without applying its schema. Call Migrate explicitly to
// create tables. Existing databases are never dropped or rebuilt automatically.
func New(opts Options) (*Runtime, error) {
	if opts.Token == "" {
		return nil, errors.New("serverless: Token is required")
	}
	u, err := url.Parse(opts.APIURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("serverless: APIURL must be an absolute HTTP(S) origin")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.DockerPath == "" {
		opts.DockerPath = "docker"
	}
	if opts.DockerImage == "" {
		opts.DockerImage = DefaultDockerImage
	}
	docker, err := exec.LookPath(opts.DockerPath)
	if err != nil {
		return nil, fmt.Errorf("serverless requires Docker: %w", err)
	}
	opts.DockerPath = docker
	checkCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	check := exec.CommandContext(checkCtx, docker, "info", "--format", "{{.OSType}}")
	if out, err := check.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("serverless requires a running Linux Docker engine: %w: %s", err, out)
	} else if strings.TrimSpace(string(out)) != "linux" {
		return nil, errors.New("serverless requires Linux Docker containers")
	}
	if err := exec.CommandContext(checkCtx, docker, "image", "inspect", opts.DockerImage).Run(); err != nil {
		return nil, fmt.Errorf("serverless: Docker image %q is unavailable; pull it before starting: %w", opts.DockerImage, err)
	}
	modules, err := readModules(filepath.Join(opts.ProjectDir, "tgcloud"))
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "tgfake-serverless-")
	if err != nil {
		return nil, err
	}
	r := &Runtime{opts: opts, modules: modules, tempDir: tempDir}
	if err := os.WriteFile(filepath.Join(tempDir, "runner.mjs"), []byte(databaseSDK+"\n"+platformSDK+"\n"+runner), 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	if opts.DatabasePath == "" {
		r.opts.DatabasePath = filepath.Join(r.tempDir, "bot.sqlite")
	}
	r.opts.DatabasePath, err = filepath.Abs(r.opts.DatabasePath)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(r.opts.DatabasePath), 0o700)
	}
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	return r, nil
}

func readModules(root string) (map[string]string, error) {
	modules := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("serverless: symlink module %s is not supported", path)
		}
		if entry.IsDir() || filepath.Ext(path) != ".js" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".js")
		parts := strings.Split(name, "/")
		valid := name == "schema" || (len(parts) >= 2 && parts[0] == "lib") ||
			(len(parts) == 2 && (parts[0] == "handlers" || (parts[0] == "endpoints" && endpointName.MatchString(parts[1]))))
		if !valid {
			return fmt.Errorf("serverless: invalid module %s", rel)
		}
		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied project
		if err == nil {
			modules[name] = string(data)
		}
		return err
	})
	return modules, err
}

// Close cancels running invocations and removes a temporary database, if any.
// It is safe to call repeatedly or while a module is running.
func (r *Runtime) Close() {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tempDir != "" {
		_ = os.RemoveAll(r.tempDir)
		r.tempDir = ""
	}
}

// Run calls a handler or endpoint with an object payload and a context object.
// It is the local equivalent of tgcloud run: endpoint initData is not validated
// here. Use Handler for authenticated Mini App calls.
func (r *Runtime) Run(ctx context.Context, module string, input, invocationContext map[string]any) (Result, error) {
	return r.invoke(ctx, module, input, invocationContext, false)
}

// Migrate creates new tables and indexes in schema.js in a transaction. It
// refuses changes to existing columns and never drops objects; use reviewed raw
// SQL for migrations beyond initial table and index creation.
func (r *Runtime) Migrate(ctx context.Context) error {
	if _, ok := r.modules["schema"]; !ok {
		return nil
	}
	_, err := r.invoke(ctx, "schema", nil, nil, true)
	return err
}

func (r *Runtime) invoke(ctx context.Context, module string, input, invocationContext map[string]any, migrate bool) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	module = strings.TrimSuffix(module, ".js")
	if _, ok := r.modules[module]; !ok {
		return Result{}, fmt.Errorf("serverless: module %q not found", module)
	}
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	if err := r.ctx.Err(); err != nil {
		return Result{}, err
	}
	if input == nil {
		input = map[string]any{}
	}
	if invocationContext == nil {
		invocationContext = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{
		"modules": r.modules, "module": module, "input": input, "context": invocationContext,
		"database": "/data/" + filepath.Base(r.opts.DatabasePath), "migrate": migrate,
		"apiURL": r.opts.APIURL, "token": r.opts.Token,
	})
	if err != nil {
		return Result{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Result{}, err
	}
	name := fmt.Sprintf("tgfake-serverless-%x", nonce)
	cmd := exec.CommandContext(ctx, r.opts.DockerPath, "run", "--rm", "--init", "--name", name, "-i",
		"--mount", "type=bind,source="+r.tempDir+",target=/runtime,readonly",
		"--mount", "type=bind,source="+filepath.Dir(r.opts.DatabasePath)+",target=/data",
		"--entrypoint", "node", r.opts.DockerImage, "--experimental-vm-modules", "--no-warnings", "/runtime/runner.mjs")
	// Killing docker's client does not necessarily stop its container.
	defer func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = exec.CommandContext(cleanupCtx, r.opts.DockerPath, "rm", "-f", name).Run()
	}()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = stdin.Close() }()
	var stderr limitedBuffer
	protocol := &processProtocol{ctx: ctx, runtime: r, stdin: stdin}
	cmd.Stdout, cmd.Stderr = protocol, &stderr
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	if _, err := stdin.Write(append(payload, '\n')); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Result{}, err
	}
	if err := cmd.Wait(); err != nil {
		contextErr := ctx.Err()
		cancel()
		protocol.workers.Wait()
		if contextErr != nil {
			return Result{}, contextErr
		}
		return Result{}, fmt.Errorf("serverless: Docker runtime: %w: %s", err, stderr.String())
	}
	cancel()
	protocol.workers.Wait()
	var response struct {
		Result
		Error *InvocationError `json:"error"`
	}
	if err := json.Unmarshal(protocol.response, &response); err != nil {
		return Result{}, fmt.Errorf("serverless: invalid runtime response: %w", err)
	}
	if r.opts.Logf != nil {
		for _, line := range response.Logs {
			r.opts.Logf("serverless %s %s: %s", module, line.Level, line.Text)
		}
	}
	if response.Error != nil {
		return response.Result, response.Error
	}
	return response.Result, nil
}

// Limit process output without allowing a logging loop to exhaust Go memory.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const max = 8 << 20
	if len(p) > max-b.Len() {
		return 0, errors.New("serverless: output exceeds 8 MiB")
	}
	return b.Buffer.Write(p)
}

// Bot API calls cross the container's stdin/stdout channel. Neither Docker host
// networking nor a publicly reachable tgfake listener is needed.
type processProtocol struct {
	ctx      context.Context
	runtime  *Runtime
	stdin    io.Writer
	pending  []byte
	response json.RawMessage
	writeMu  sync.Mutex
	workers  sync.WaitGroup
}

func (p *processProtocol) send(value any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return json.NewEncoder(p.stdin).Encode(value)
}

func (p *processProtocol) Write(data []byte) (int, error) {
	if len(p.pending)+len(data) > 128<<20 {
		return 0, errors.New("serverless: protocol message exceeds 128 MiB")
	}
	p.pending = append(p.pending, data...)
	for {
		line, rest, ok := bytes.Cut(p.pending, []byte{'\n'})
		if !ok {
			break
		}
		var event struct {
			Type   string                     `json:"type"`
			ID     int                        `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
			Files  []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
				Data string `json:"data"`
			} `json:"files"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return 0, fmt.Errorf("serverless: invalid protocol message: %w", err)
		}
		switch event.Type {
		case "result":
			if len(line) > 8<<20 {
				return 0, errors.New("serverless: result exceeds 8 MiB")
			}
			p.response = append(json.RawMessage(nil), line...)
		case "fetch":
			p.workers.Add(1)
			go func(id int, method, rawURL, body string, headers map[string]string) {
				defer p.workers.Done()
				p.fetch(id, method, rawURL, body, headers)
			}(event.ID, event.Method, event.URL, event.Body, event.Headers)
		case "api":
			params := url.Values{}
			for key, raw := range event.Params {
				var str string
				if json.Unmarshal(raw, &str) == nil {
					params.Set(key, str)
				} else {
					params.Set(key, string(raw))
				}
			}
			var body bytes.Buffer
			contentType := "application/x-www-form-urlencoded"
			if len(event.Files) == 0 {
				body.WriteString(params.Encode())
			} else {
				writer := multipart.NewWriter(&body)
				for key := range params {
					if err := writer.WriteField(key, params.Get(key)); err != nil {
						return 0, err
					}
				}
				for _, file := range event.Files {
					part, err := writer.CreateFormFile(file.Key, file.Name)
					if err != nil {
						return 0, err
					}
					raw, err := base64.StdEncoding.DecodeString(file.Data)
					if err != nil {
						return 0, err
					}
					if _, err := part.Write(raw); err != nil {
						return 0, err
					}
				}
				if err := writer.Close(); err != nil {
					return 0, err
				}
				contentType = writer.FormDataContentType()
			}
			if event.Method == "" || strings.ContainsAny(event.Method, "/?#") {
				return 0, errors.New("serverless: invalid Bot API method")
			}
			req, err := http.NewRequestWithContext(p.ctx, http.MethodPost,
				strings.TrimRight(p.runtime.opts.APIURL, "/")+"/bot"+p.runtime.opts.Token+"/"+event.Method, &body)
			if err != nil {
				return 0, err
			}
			req.Header.Set("Content-Type", contentType)
			resp, err := http.DefaultClient.Do(req)
			var response json.RawMessage
			if err != nil {
				response, _ = json.Marshal(map[string]any{"ok": false, "error_code": 500, "description": err.Error()})
			} else {
				response, err = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
				_ = resp.Body.Close()
				if err != nil {
					return 0, err
				}
				if !json.Valid(response) {
					return 0, errors.New("serverless: Bot API returned invalid JSON")
				}
			}
			if err := p.send(map[string]any{"id": event.ID, "body": response}); err != nil {
				return 0, err
			}
		default:
			return 0, errors.New("serverless: unknown protocol message")
		}
		p.pending = rest
	}
	return len(data), nil
}

func (p *processProtocol) fetch(id int, method, rawURL, body string, headers map[string]string) {
	fail := func(err error) { _ = p.send(map[string]any{"id": id, "type": "fetchError", "error": err.Error()}) }
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		fail(errors.New("SDK fetch requires an absolute HTTP(S) URL"))
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		fail(err)
		return
	}
	req, err := http.NewRequestWithContext(p.ctx, method, rawURL, bytes.NewReader(raw))
	if err != nil {
		fail(err)
		return
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	responseHeaders := map[string]string{}
	for key := range resp.Header {
		responseHeaders[key] = strings.Join(resp.Header.Values(key), ", ")
	}
	if err := p.send(map[string]any{"id": id, "type": "fetchHead", "status": resp.StatusCode,
		"statusText": http.StatusText(resp.StatusCode), "url": resp.Request.URL.String(), "headers": responseHeaders}); err != nil {
		return
	}
	buffer := make([]byte, 32<<10)
	total := 0
	for {
		n, err := resp.Body.Read(buffer)
		total += n
		if total > 30<<20 {
			fail(errors.New("HTTP response exceeds 30 MB"))
			return
		}
		if n > 0 {
			if err := p.send(map[string]any{"id": id, "type": "fetchChunk", "data": base64.StdEncoding.EncodeToString(buffer[:n])}); err != nil {
				return
			}
		}
		if errors.Is(err, io.EOF) {
			_ = p.send(map[string]any{"id": id, "type": "fetchEnd"})
			return
		}
		if err != nil {
			fail(err)
			return
		}
	}
}

// Poll delivers updates to handlers/<type>, passing the unwrapped payload and
// ctx.update. Only non-empty handlers are subscribed. Failed invocations are
// logged and consumed; Poll returns transport errors and stops on cancellation.
func (r *Runtime) Poll(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	var kinds []string
	for name, source := range r.modules {
		if strings.HasPrefix(name, "handlers/") && strings.TrimSpace(source) != "" {
			kinds = append(kinds, strings.TrimPrefix(name, "handlers/"))
		}
	}
	if len(kinds) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	allowed, _ := json.Marshal(kinds)
	offset := 0
	for {
		params := url.Values{"offset": {strconv.Itoa(offset)}, "timeout": {"1"}, "allowed_updates": {string(allowed)}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.opts.APIURL, "/")+"/bot"+r.opts.Token+"/getUpdates", strings.NewReader(params.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		var envelope struct {
			OK          bool             `json:"ok"`
			Result      []map[string]any `json:"result"`
			Description string           `json:"description"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if !envelope.OK {
			return fmt.Errorf("serverless: getUpdates: %s", envelope.Description)
		}
		for _, update := range envelope.Result {
			for _, kind := range kinds {
				if input, ok := update[kind].(map[string]any); ok {
					if _, err := r.Run(ctx, "handlers/"+kind, input, map[string]any{"update": update}); err != nil && r.opts.Logf != nil {
						r.opts.Logf("serverless handlers/%s: %v", kind, err)
					}
				}
			}
			if id, ok := update["update_id"].(float64); ok {
				offset = int(id) + 1
			}
		}
	}
}

// Handler serves POST /api/<endpoint> with the official Mini App SDK's
// Authorization: TMA <base64(initData)> header and {ok,result} envelope.
// initData must be signed by Token and include a non-zero Telegram user id.
func (r *Runtime) Handler() http.Handler {
	return http.HandlerFunc(r.serveEndpoint)
}

func (r *Runtime) serveEndpoint(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		endpointError(w, 405, "METHOD_NOT_ALLOWED", "Method Not Allowed", nil)
		return
	}
	name := strings.TrimPrefix(req.URL.Path, "/api/")
	if !strings.HasPrefix(req.URL.Path, "/api/") || !endpointName.MatchString(name) {
		endpointError(w, 404, "NOT_FOUND", "Not Found", nil)
		return
	}
	encoded, ok := strings.CutPrefix(req.Header.Get("Authorization"), "TMA ")
	initData, err := base64.StdEncoding.DecodeString(encoded)
	if !ok || err != nil {
		endpointError(w, 401, "UNAUTHORIZED", "Unauthorized", nil)
		return
	}
	fields, err := webapp.Validate(string(initData), r.opts.Token)
	var user map[string]any
	if err != nil || json.Unmarshal([]byte(fields.Get("user")), &user) != nil {
		endpointError(w, 401, "UNAUTHORIZED", "Unauthorized", nil)
		return
	}
	if id, ok := user["id"].(float64); !ok || id <= 0 {
		endpointError(w, 401, "UNAUTHORIZED", "Unauthorized", nil)
		return
	}
	if strings.TrimSpace(r.modules["endpoints/"+name]) == "" {
		endpointError(w, 404, "NOT_FOUND", "Not Found", nil)
		return
	}
	var input map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20))
	if decoder.Decode(&input) != nil || input == nil || decoder.Decode(new(any)) != io.EOF {
		endpointError(w, 400, "BAD_REQUEST", "Bad Request", nil)
		return
	}
	parsed := make(map[string]any, len(fields))
	for key := range fields {
		var value any
		switch key {
		case "user", "receiver", "chat", "auth_date", "can_send_after":
			if json.Unmarshal([]byte(fields.Get(key)), &value) != nil {
				endpointError(w, 401, "UNAUTHORIZED", "Unauthorized", nil)
				return
			}
		default:
			value = fields.Get(key)
		}
		parsed[key] = value
	}
	res, err := r.Run(req.Context(), "endpoints/"+name, input, map[string]any{"initData": parsed})
	if err != nil {
		var invocation *InvocationError
		if errors.As(err, &invocation) && invocation.Type == "ENDPOINT_ERROR" {
			endpointError(w, 400, invocation.Type, invocation.Description, invocation.Parameters)
		} else {
			if r.opts.Logf != nil {
				r.opts.Logf("serverless endpoints/%s: %v", name, err)
			}
			endpointError(w, 500, "INTERNAL_ERROR", "Internal Server Error", nil)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res.Value})
}

func endpointError(w http.ResponseWriter, status int, kind, description string, parameters json.RawMessage) {
	body := map[string]any{"ok": false, "error_code": status, "error_type": kind, "description": description}
	if len(parameters) > 0 && string(parameters) != "null" {
		body["parameters"] = parameters
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
