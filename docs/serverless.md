# Local Telegram Serverless

`--serverless` runs the JavaScript modules of a [Telegram Serverless](https://core.telegram.org/bots/serverless) project against tgfake. Each invocation runs Node.js in a Linux Docker container, with a fresh module context and a shared SQLite database. The Go packages still use only the standard library. Docker is required only for this mode; Node.js does not need to be installed on the host.

## Start a project

Pull the runtime image once, then start the included [counter project](../examples/serverless):

```bash
docker pull node:24-bookworm-slim
tgfake --serverless examples/serverless --serverless-migrate
```

Open the chat page or inject a message:

```bash
curl -sf -X POST http://127.0.0.1:18790/sim/message \
  -H 'Content-Type: application/json' -d '{"text":"hello"}'
curl -sf 'http://127.0.0.1:18790/sim/chat/4242?format=text'
```

The bot answers `Hello! I've seen 1 message(s) from you.` Its next message increments the counter. A handler runs asynchronously, so a script should wait for the expected transcript or outbox call, as the [CI example](ci.md#serverless-projects) does.

Without `--token`, this mode uses the fake token `123456:SERVERLESS` for both Bot API calls and Mini App signatures. Set `--token` explicitly to use another fake token. The normal mode still accepts any token by default.

`--serverless-db bot.sqlite` keeps data across command restarts. Without it, the database is temporary and removed on shutdown. `POST /sim/reset` resets the Telegram stand, but leaves the project's database intact. `--serverless-migrate` explicitly creates schema tables and indexes; starting without it never applies schema changes.

## Project modules

The project argument is the directory containing `tgcloud/`:

```text
project/
  tgcloud/
    schema.js
    handlers/message.js
    handlers/callback_query.js
    endpoints/getProfile.js
    lib/reply.js
    lib/internal/format.js
```

Handlers and endpoints are flat; shared modules may be nested. Only `.js` modules in these locations are loaded. Invalid layouts and symbolic links are refused. A relative import must have a `.js` extension and stay inside `tgcloud/`. Module-name imports such as `lib/reply` and the SDK imports `sdk`, `sdk/db`, `sdk/api`, `sdk/fetch` are also available. Node.js built-ins, npm packages, host filesystem access and dynamic imports are unavailable to project modules.

Modules are loaded as a snapshot at startup. Restart tgfake after editing them. A non-empty `handlers/<type>.js` subscribes to that update type. Its default function receives the unwrapped payload and a second argument containing `ctx.update`, the complete update. An empty or absent handler is ignored. Only the update types the [stand creates](bot-api.md#getupdates) can be injected.

The runtime uses `getUpdates` internally. Do not start a second poller for the same stand. Handler exceptions are logged and the update is consumed; a polling transport error stops the command. Each invocation, including container startup, has a 10-second timeout, configurable with `--serverless-timeout`. Invocations are serialized, and global JavaScript state is fresh each time.

## SDK

```javascript
import { api, db, fetch, InputFile, FormData, BotApiError, EndpointError } from 'sdk';
import { table, integer, text, eq, sql } from 'sdk/db';
```

- `api.<method>(params)` and `api.call(method, params)` call the configured Bot API origin. `BotApiError` carries `code`, `description`, `method`, and `parameters`, including injected `retry_after`. `InputFile` supports top-level uploads and `attach://` references nested in parameters. The available methods and refusals remain those of [bot-api.md](bot-api.md).
- `api.getFileContent(file_id)` and `api.getFileStream(file_id)` use `getFile` and its download path, with a 20 MiB limit. tgfake currently does not implement `getFile`, so these helpers require a Go runtime configured with another origin which does. The stream has an async iterator and `bytes()`.
- `fetch` supports HTTP(S), request methods, headers, JSON/form/text body helpers, raw bytes, `InputFile`, and `FormData` uploads. Responses expose status, URL, headers, `text()`, `json()`, and an async iterator over `body`. The response limit is 30 MiB, including streamed responses. HTTP and Bot API requests go through the host over stdin/stdout, so loopback services, including `--llm`, are reachable without Docker host networking. Request options beyond method, headers and body are not emulated.
- `console.log/debug/info/warn/error/trace` are captured with their level and printed to stderr by the command. Console text is limited to 1 MiB per invocation; results are limited to 8 MiB.

The database supports `table`, `text`, `integer`, `real`/`float`, `numeric`, `blob`, `boolean`, `json`, defaults, primary keys, uniqueness, generated columns, indexes and checks. Table-bound queries convert booleans, JSON, timestamps and bytes; raw SQL preserves SQLite values. Foreign-key declarations throw, matching Telegram's restriction.

Queries support `select().from()`, projections, filters, ordering, grouping, having, distinct, limit/offset, `all()`, `get()`, and positional `values()`. Writes support insert batches, update, delete, conflict handling, and `returning().run()`. `db.$count`, raw `run/all/get/values`, bound `sql` fragments and the comparison, aggregate and ordering operators are available. Always await a terminal method.

Migration is deliberately limited to creating new tables and indexes in a transaction. Existing-column, constraint and index changes are refused when detected; missing declarations never drop stored objects. Deprecation and schema SQL with bound parameters require manual migration. For changes beyond initial creation, use reviewed raw SQL or a separately managed test database. This is not the cloud migration planner's safe/warning/manual workflow.

## Mini App endpoints

`POST /api/<name>` calls `tgcloud/endpoints/<name>.js`. Names follow `[A-Za-z_][A-Za-z0-9_]*`. The input must be a JSON object, up to 1 MiB. The official `Telegram.WebApp.Serverless.call` sends the authentication header and JSON body in this format:

```text
Authorization: TMA <base64(initData)>
Content-Type: application/json
```

The runtime verifies the signature with its token and requires a non-zero user id before running the endpoint. The second argument carries parsed `ctx.initData`, including the user, chat, start parameter and authentication date. As with `webapp.Validate`, there is no authentication-date expiration policy in this fake.

Success is `{"ok":true,"result":...}`. An `EndpointError` returns HTTP 400 with `error_type: "ENDPOINT_ERROR"`, its description and optional parameters. Any other exception returns HTTP 500 with a generic description; the real message is logged. Missing or invalid authentication returns HTTP 401 with `error_type: "UNAUTHORIZED"`. These envelopes work with the official Mini Apps SDK. Exact undocumented cloud error wording is not emulated.

The chat page remains at `/`; static Mini App hosting and `tgcloud.jsonc` serving rules are not implemented. For browser testing, serve or proxy your Mini App and `/api/` from the same origin, because the official SDK uses relative endpoint URLs. CLI login, deploy, revision tracking, cloud webhook management and BotFather are outside this local execution mode.

## Go tests

```go
runtime, err := serverless.New(serverless.Options{
    ProjectDir: "testdata/project",
    APIURL: srv.URL, // a server.New(...).Handler() on httptest
    Token: "123:TEST",
})
if err != nil { t.Fatal(err) }
t.Cleanup(runtime.Close)
if err := runtime.Migrate(ctx); err != nil { t.Fatal(err) }
result, err := runtime.Run(ctx, "handlers/message",
    map[string]any{"chat": map[string]any{"id": 4242}, "text": "hi"},
    map[string]any{"update": map[string]any{"update_id": 1}},
)
```

`Run` accepts handler/endpoint module names with an optional `.js` suffix. Like `tgcloud run`, it does not authenticate the supplied context. `Result` contains the JSON return value and console logs; module exceptions are `*serverless.InvocationError`. `Poll(ctx)` runs the update loop. Mount `runtime.Handler()` at `/api/` for authenticated endpoints. `Close()` cancels invocations, removes temporary files and waits for container cleanup; a configured database is retained.

`Options` additionally exposes `DockerPath`, `DockerImage`, `DatabasePath`, `Timeout` and `Logf`. The image must provide Node.js 22.13 or newer with `node:sqlite`; the default is `serverless.DefaultDockerImage`. Pull the image before `New`. Bind mounts require a local Linux Docker engine, such as Docker Desktop in Linux-container mode; a remote daemon cannot read the host's temporary paths.

Local projects are trusted code. The VM module restrictions and Docker container are useful isolation for tests, but this runtime is not a security boundary for hostile JavaScript.
