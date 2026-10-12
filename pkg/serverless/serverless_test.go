package serverless

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/EvilFreelancer/tgfake/pkg/webapp"
)

func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", DefaultDockerImage).Run(); err != nil {
		if os.Getenv("TGFAKE_TEST_DOCKER") == "1" {
			t.Fatalf("Docker integration is required: %v", err)
		}
		t.Skip("requires Docker and a pulled " + DefaultDockerImage)
	}
}

func writeProject(t *testing.T, modules map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, source := range modules {
		path := filepath.Join(root, "tgcloud", filepath.FromSlash(name)+".js")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func newRuntime(t *testing.T, project string) (*Runtime, *server.Server) {
	t.Helper()
	requireDocker(t)
	fake := server.New(server.Options{Token: "123:TEST", MaxPollWait: 100 * time.Millisecond})
	srv := httptest.NewServer(fake.Handler())
	r, err := New(Options{ProjectDir: project, APIURL: srv.URL, Token: "123:TEST", Timeout: 10 * time.Second})
	if err != nil {
		fake.Close()
		srv.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); fake.Close(); srv.Close() })
	return r, fake
}

func runModule(t *testing.T, r *Runtime, name string, input, invocationContext map[string]any) Result {
	t.Helper()
	result, err := r.Run(context.Background(), name, input, invocationContext)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func TestDatabaseAndSDK(t *testing.T) {
	project := writeProject(t, map[string]string{
		"schema": `import {table, integer, text, boolean, json, blob, index} from 'sdk/db';
export const users = table('users', {
 id: integer().primaryKey({autoIncrement:true}), name: text().notNull(),
 enabled: boolean().default(false), prefs: json(), bytes: blob(),
 created: integer(undefined, {mode:'timestamp'}),
}, t => ({idx: index('users_name').on(t.name)}));`,
		"handlers/message": `import {api, db, BotApiError, InputFile} from 'sdk';
import {eq, sql, and, desc, count, inArray} from 'sdk/db';
import {users} from '../schema.js';
export default async function(input, ctx) {
 const [row] = await db.insert(users).values({name: input.name, enabled:true, prefs:{lang:'en'}, bytes:new Uint8Array([1,2]), created:new Date(100000)}).returning().run();
 await db.update(users).set({name:'Ann'}).where(eq(users.id,row.id)).run();
 const found = await db.select().from(users).where(and(eq(users.enabled,true), inArray(users.id,[row.id]))).orderBy(desc(users.id)).limit(1).get();
 if (!found.enabled || found.prefs.lang !== 'en' || found.bytes[1] !== 2 || found.created.getTime() !== 100000) throw new Error('Column conversion failed');
 const projected = await db.select({n:count()}).from(users).get();
 const arrays = await db.select({name:users.name}).from(users).values();
 const raw = await db.get(sql` + "`SELECT name FROM users WHERE id = ${row.id}`" + `);
 await db.run('UPDATE users SET name = :name WHERE id = :id', {':name':'Alice', ':id':row.id});
 let error;
 try { await api.sendMessage({chat_id:4242,text:''}); } catch(e) {
   if (!(e instanceof BotApiError)) throw e;
   error = {code:e.code, description:e.description, method:e.method};
 }
 const sent = await api.sendDocument({chat_id:4242,document:new InputFile(new TextEncoder().encode('report'),'report.txt')});
 const downloaded = new TextDecoder().decode(await api.getFileContent(sent.document.file_id));
 await db.delete(users).where(eq(users.id,row.id)).returning().run();
 console.info('received', ctx.update.update_id);
 return {raw:raw.name, arrays, count:projected.n, remaining:await db.$count(users), error, downloaded};
}`,
	})
	r, fake := newRuntime(t, project)
	if err := r.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Applying the same schema again must be harmless.
	if err := r.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := runModule(t, r, "handlers/message", map[string]any{"name": "Alice"}, map[string]any{"update": map[string]any{"update_id": 7}})
	var body map[string]any
	if err := json.Unmarshal(result.Value, &body); err != nil {
		t.Fatal(err)
	}
	if body["raw"] != "Ann" || body["count"] != float64(1) || body["remaining"] != float64(0) || body["downloaded"] != "report" {
		t.Fatalf("database result: %s", result.Value)
	}
	apiError := body["error"].(map[string]any)
	if apiError["code"] != float64(400) || apiError["description"] != "Bad Request: message text is empty" || apiError["method"] != "sendMessage" {
		t.Fatalf("BotApiError: %v", apiError)
	}
	if len(result.Logs) != 1 || result.Logs[0].Level != "info" || result.Logs[0].Text != "received 7" {
		t.Fatalf("logs: %v", result.Logs)
	}
	if data, ok := fake.File("file_1"); !ok || string(data) != "report" {
		t.Fatalf("InputFile did not reach the stand: %q", data)
	}
}

func authorization(token string, user string) string {
	fields := url.Values{"auth_date": {fmt.Sprint(time.Now().Unix())}, "user": {user}, "start_param": {"demo"}}
	fields.Set("hash", webapp.Sign(token, fields))
	return "TMA " + base64.StdEncoding.EncodeToString([]byte(fields.Encode()))
}

func TestEndpoints(t *testing.T) {
	project := writeProject(t, map[string]string{
		"endpoints/profile": `export default (input,ctx) => ({id:ctx.initData.user.id,lang:input.lang,start:ctx.initData.start_param});`,
		"endpoints/refuse":  `import {EndpointError} from 'sdk'; export default () => {throw new EndpointError('Out of stock',{code:'EMPTY'});};`,
		"endpoints/fail":    `export default () => {throw new Error('private database credentials');};`,
	})
	r, _ := newRuntime(t, project)
	for _, tc := range []struct {
		name, endpoint, auth, input string
		status                      int
		kind, description           string
	}{
		{"success", "profile", authorization("123:TEST", `{"id":4242}`), `{"lang":"en"}`, 200, "", ""},
		{"missing auth", "profile", "", `{}`, 401, "UNAUTHORIZED", "Unauthorized"},
		{"wrong token", "profile", authorization("wrong", `{"id":4242}`), `{}`, 401, "UNAUTHORIZED", "Unauthorized"},
		{"missing user", "profile", authorization("123:TEST", `{}`), `{}`, 401, "UNAUTHORIZED", "Unauthorized"},
		{"malformed user", "profile", authorization("123:TEST", `null`), `{}`, 401, "UNAUTHORIZED", "Unauthorized"},
		{"non-object input", "profile", authorization("123:TEST", `{"id":4242}`), `[]`, 400, "BAD_REQUEST", "Bad Request"},
		{"null input", "profile", authorization("123:TEST", `{"id":4242}`), `null`, 400, "BAD_REQUEST", "Bad Request"},
		{"trailing input", "profile", authorization("123:TEST", `{"id":4242}`), `{} {}`, 400, "BAD_REQUEST", "Bad Request"},
		{"unknown endpoint", "absent", authorization("123:TEST", `{"id":4242}`), `{}`, 404, "NOT_FOUND", "Not Found"},
		{"endpoint error", "refuse", authorization("123:TEST", `{"id":4242}`), `{}`, 400, "ENDPOINT_ERROR", "Out of stock"},
		{"internal error", "fail", authorization("123:TEST", `{"id":4242}`), `{}`, 500, "INTERNAL_ERROR", "Internal Server Error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/"+tc.endpoint, strings.NewReader(tc.input))
			req.Header.Set("Authorization", tc.auth)
			w := httptest.NewRecorder()
			r.Handler().ServeHTTP(w, req)
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tc.status != 200 {
				if body["error_type"] != tc.kind || body["description"] != tc.description || body["error_code"] != float64(tc.status) {
					t.Fatalf("error envelope: %s", w.Body)
				}
				if tc.kind == "ENDPOINT_ERROR" && body["parameters"].(map[string]any)["code"] != "EMPTY" {
					t.Fatalf("missing error parameters: %s", w.Body)
				}
			} else if result := body["result"].(map[string]any); result["id"] != float64(4242) || result["lang"] != "en" || result["start"] != "demo" {
				t.Fatalf("endpoint context: %s", w.Body)
			}
		})
	}
}

func TestModuleRulesAndCancellation(t *testing.T) {
	project := writeProject(t, map[string]string{
		"lib/internal/reply":  `export const reply = input => input.text;`,
		"handlers/message":    `import {reply} from '../lib/internal/reply.js'; export default input => reply(input);`,
		"endpoints/node":      `import fs from 'node:fs'; export default () => fs;`,
		"endpoints/escape":    `import x from '../../outside.js'; export default () => x;`,
		"endpoints/extension": `import x from '../lib/internal/reply'; export default () => x;`,
		"endpoints/cycle":     `import {a} from '../lib/a.js'; export default () => a();`,
		"lib/a":               `import {b} from './b.js'; export const a = () => b();`,
		"lib/b":               `import {a} from './a.js'; export const b = () => 'cycle';`,
		"endpoints/loop":      `import {api} from 'sdk'; export default async () => {await api.sendMessage({chat_id:4242,text:'started'}); while(true) {}};`,
	})
	r, fake := newRuntime(t, project)
	result := runModule(t, r, "handlers/message", map[string]any{"text": "hello"}, nil)
	if string(result.Value) != `"hello"` {
		t.Fatalf("relative module: %s", result.Value)
	}
	if result := runModule(t, r, "endpoints/cycle", nil, nil); string(result.Value) != `"cycle"` {
		t.Fatalf("cyclic modules: %s", result.Value)
	}
	for name, want := range map[string]string{"node": "not available", "escape": "escapes tgcloud", "extension": ".js extension"} {
		_, err := r.Run(context.Background(), "endpoints/"+name, nil, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Run(ctx, "endpoints/loop", nil, nil); done <- err }()
	if !fake.WaitCall("sendMessage", 1, 5*time.Second) {
		t.Fatal("loop module never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled invocation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("container did not stop on cancellation")
	}
	r.Close()
	if _, err := os.Stat(r.opts.DatabasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary database survived Close: %v", err)
	}
	if _, err := r.Run(context.Background(), "handlers/message", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed runtime accepted invocation: %v", err)
	}
}

func TestSchemaIsExplicitAndMigrationsAreNonDestructive(t *testing.T) {
	project := writeProject(t, map[string]string{
		"schema":          `import {table,integer,text} from 'sdk/db'; export const items=table('items',{id:integer().primaryKey(),text:text()});`,
		"endpoints/write": `import {db} from 'sdk'; import {items} from '../schema.js'; export default () => db.insert(items).values({id:1,text:'keep'}).run();`,
		"endpoints/read":  `import {db} from 'sdk'; import {items} from '../schema.js'; export default () => db.select().from(items).all();`,
	})
	r, _ := newRuntime(t, project)
	if _, err := r.Run(context.Background(), "endpoints/write", nil, nil); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("New unexpectedly applied schema: %v", err)
	}
	if err := r.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runModule(t, r, "endpoints/write", nil, nil)
	r.modules["schema"] = `import {table,integer,text} from 'sdk/db'; export const extra=table('extra',{id:integer()}); export const items=table('items',{id:integer().primaryKey(),text:integer()});`
	if err := r.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "manual migration") {
		t.Fatalf("unsafe type change accepted: %v", err)
	}
	// Reverting the module snapshot exposes the unchanged data.
	r.modules["schema"] = `import {table,integer,text} from 'sdk/db'; export const items=table('items',{id:integer().primaryKey(),text:text()});`
	result := runModule(t, r, "endpoints/read", nil, nil)
	if !strings.Contains(string(result.Value), `"keep"`) {
		t.Fatalf("failed migration lost data: %s", result.Value)
	}
}

func TestEndpointRefusesInvalidAuthorizationWithoutDocker(t *testing.T) {
	r := &Runtime{opts: Options{Token: "test"}, modules: map[string]string{"endpoints/test": "export default () => true"}}
	for _, auth := range []string{"", "TMA broken-base64", authorization("wrong", `{"id":1}`)} {
		req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{}`))
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		r.Handler().ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("auth %q: %d %s", auth, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/test", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal("endpoint accepted GET")
	}
}

func TestReadModulesRejectsInvalidLayout(t *testing.T) {
	project := writeProject(t, map[string]string{"handlers/nested/message": "export default () => {}"})
	if _, err := readModules(filepath.Join(project, "tgcloud")); err == nil {
		t.Fatal("nested handler accepted")
	}
}

func TestProcessProtocolReportsMalformedOutput(t *testing.T) {
	for _, message := range []string{"bad\n", "{\"type\":\"unknown\"}\n"} {
		p := &processProtocol{stdin: io.Discard}
		if _, err := p.Write([]byte(message)); err == nil {
			t.Fatal("malformed protocol message accepted")
		}
	}
}

func TestSDKFetchReachesLocalServicesAndStreams(t *testing.T) {
	project := writeProject(t, map[string]string{
		"endpoints/fetch": `import {fetch, FormData, InputFile} from 'sdk/fetch';
export default async input => {
 const json = await fetch(input.url+'/json',{method:'POST',body:fetch.body.json({hello:'world'})});
 const data = await json.json();
 const stream = await fetch(input.url+'/stream');
 let text=''; for await(const chunk of stream.body) text += new TextDecoder().decode(chunk);
 const form=new FormData(); form.append('file',new InputFile(new TextEncoder().encode('hello'),'hello.txt')); form.append('label','report');
 const uploaded=await fetch(input.url+'/upload',{method:'POST',body:form});
 return {data, text, uploaded:await uploaded.text(), header:json.headers.get('X-Test'), status:json.status, ok:json.ok};
}`,
		"endpoints/file": `import {api} from 'sdk'; export default async () => {
 const bytes=await api.getFileContent('file_1'); const {file,body}=await api.getFileStream('file_1');
 let text=''; for await(const chunk of body) text+=new TextDecoder().decode(chunk);
 return {content:new TextDecoder().decode(bytes),text,unique:file.file_unique_id};
};`,
	})
	r, _ := newRuntime(t, project)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/json":
			if req.Header.Get("Content-Type") != "application/json" {
				t.Error("JSON body helper omitted Content-Type")
			}
			w.Header().Set("X-Test", "yes")
			_, _ = io.Copy(w, req.Body)
		case "/stream":
			for _, chunk := range []string{"one", "two"} {
				_, _ = io.WriteString(w, chunk)
				w.(http.Flusher).Flush()
			}
		case "/upload":
			file, header, err := req.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer func() { _ = file.Close() }()
			content, _ := io.ReadAll(file)
			if header.Filename != "hello.txt" || string(content) != "hello" || req.FormValue("label") != "report" {
				t.Errorf("upload: %s %q %q", header.Filename, content, req.FormValue("label"))
			}
			_, _ = io.WriteString(w, "uploaded")
		case "/bot123:TEST/getFile":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"file_id":"file_1","file_unique_id":"unique1","file_path":"documents/file1"}}`)
		case "/file/bot123:TEST/documents/file1":
			_, _ = io.WriteString(w, "downloaded")
		default:
			w.WriteHeader(404)
		}
	}))
	defer service.Close()
	result := runModule(t, r, "endpoints/fetch", map[string]any{"url": service.URL}, nil)
	var body map[string]any
	if err := json.Unmarshal(result.Value, &body); err != nil {
		t.Fatal(err)
	}
	if body["text"] != "onetwo" || body["uploaded"] != "uploaded" || body["header"] != "yes" || body["ok"] != true || body["status"] != float64(200) {
		t.Fatalf("fetch: %s", result.Value)
	}
	// The download SDK also works against an origin which implements getFile.
	r.opts.APIURL = service.URL
	file := runModule(t, r, "endpoints/file", nil, nil)
	if err := json.Unmarshal(file.Value, &body); err != nil {
		t.Fatal(err)
	}
	if body["content"] != "downloaded" || body["text"] != "downloaded" || body["unique"] != "unique1" {
		t.Fatalf("file SDK: %s", file.Value)
	}
}

func TestDatabaseSurvivesRuntimeRestartAndGlobalsAreFresh(t *testing.T) {
	project := writeProject(t, map[string]string{
		"schema": `import {table,integer} from 'sdk/db'; export const counts=table('counts',{id:integer().primaryKey(),n:integer()});`,
		"endpoints/count": `import {db} from 'sdk'; import {sql} from 'sdk/db'; import {counts} from '../schema.js';
let globalCount=0;
export default async () => {
 const [row]=await db.insert(counts).values({id:1,n:1}).onConflictDoUpdate({target:counts.id,set:{n:sql` + "`${counts.n}+1`" + `}}).returning().run();
 return {persisted:row.n,global:++globalCount};
};`,
	})
	r, _ := newRuntime(t, project)
	// Put the SQLite file outside the runtime's disposable script directory.
	database := filepath.Join(t.TempDir(), "persistent.sqlite")
	r.opts.DatabasePath = database
	if err := r.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := runModule(t, r, "endpoints/count", nil, nil)
	if string(first.Value) != `{"persisted":1,"global":1}` {
		t.Fatalf("first invocation: %s", first.Value)
	}
	opts := r.opts
	r.Close()
	if _, err := os.Stat(database); err != nil {
		t.Fatal("configured database was removed:", err)
	}
	restarted, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	second := runModule(t, restarted, "endpoints/count", nil, nil)
	if string(second.Value) != `{"persisted":2,"global":1}` {
		t.Fatalf("restarted runtime: %s", second.Value)
	}
}

func TestContainerFailureKeepsItsDiagnostic(t *testing.T) {
	project := writeProject(t, map[string]string{"endpoints/test": "export default () => true"})
	r, _ := newRuntime(t, project)
	if err := os.WriteFile(filepath.Join(r.tempDir, "runner.mjs"), []byte("process.stderr.write('runtime failed'); process.exit(7);"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := r.Run(context.Background(), "endpoints/test", nil, nil)
	if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "runtime failed") {
		t.Fatalf("container failure lost its diagnostic: %v", err)
	}
}
