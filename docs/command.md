# The tgfake command

This page is the reference of the `tgfake` command in [`cmd/tgfake`](../cmd/tgfake/main.go): every flag with its default, the banner it prints, how it reports its version, how it stops and what it exits with. The HTTP surfaces the command serves are described in [bot-api.md](bot-api.md), [sim-api.md](sim-api.md) and [scripted-model.md](scripted-model.md).

## Synopsis

```text
tgfake [flags]
```

The command serves one HTTP listener with three things on it: the fake Bot API under `/bot<token>/<method>`, the simulation API under `/sim/` and the chat page at `/`. With `--llm` it also serves the scripted OpenAI-compatible model under `/v1/`. It runs until it receives SIGINT or SIGTERM.

Flags are parsed by Go's `flag` package, so `-addr` and `--addr` are the same flag, a value can follow as the next argument or after `=`, and a boolean flag is switched off only as `--llm=false`. The command takes flags only. The parser would stop at the first argument that is not a flag and drop everything after it, so such an argument is an error (`unexpected argument`) instead.

## Server flags

| Flag | Default | Meaning |
|------|---------|---------|
| `--addr` | `127.0.0.1:18790` | Address to listen on. Port `0` picks a free port, and the banner names the one picked. |
| `--token` | empty | The only bot token accepted in `/bot<token>/` paths. Empty accepts any token. When set, a call with any other token is answered `401 Unauthorized`, and Mini App launch data is signed with this token. |
| `--bot-username` | `tgfake_bot` | The username `getMe` reports and the bot's messages carry. A group message that mentions the bot uses it. |
| `--bot-name` | `tgfake` | The bot's first name, as `getMe` and the bot's own messages report it. |
| `--poll-max` | `30s` | The longest a `getUpdates` request is held open, whatever `timeout` the bot asks for. A call reaches the outbox when it is answered, so a short value such as `1s` lets a test see an idle bot's polls without waiting 30 seconds. |
| `--verbose` | off | Print one line per Bot API call to stderr. |
| `--version` | off | Print the version and exit. |

The bot's user id is not a flag: `getMe` always reports `7000000001` from the command. A Go test can change it through `server.Options.BotID` ([go.md](go.md)).

### --token

Without `--token` the stand accepts whatever token the bot puts into the path and remembers the one of the latest call, because that is the token a Mini App launch is signed with ([mini-apps.md](mini-apps.md)). With `--token 123:secret`:

```bash
curl -s http://127.0.0.1:18790/bot999:other/getMe
```

```json
{"ok":false,"error_code":401,"description":"Unauthorized"}
```

The refused call is still recorded in the outbox of the simulation API with status 401.

### --verbose

Each Bot API call the stand answers is logged to stderr with a timestamp, the method as the bot spelled it, the HTTP status and the parameters as `key=value` pairs sorted by key. Values longer than 80 bytes are cut and end with `…`, and newlines in values are printed as `\n`. Calls to paths that name no method (a 404 for a path outside `/bot<token>/<method>`) are not logged.

```text
2026/10/07 18:04:51 getUpdates 200 allowed_updates=["message","callback_query"] offset=0 timeout=30
2026/10/07 18:04:58 sendMessage 200 chat_id=4242 reply_to_message_id=1 text=You said: hello
2026/10/07 18:04:58 answerCallbackQuery 400 callback_query_id=cbq-1 text=Done
```

## Scripted model flags

These flags configure the model described in [scripted-model.md](scripted-model.md). They need `--llm`: an `--llm-*` flag given without it is an error, so a script that asks for a model never gets a stand without one.

| Flag | Default | Meaning |
|------|---------|---------|
| `--llm` | off | Also serve a scripted OpenAI-compatible model under `/v1`. |
| `--llm-model` | `tgfake-demo` | The model id `/v1/models` lists and the answers carry. |
| `--llm-script` | empty | A JSON file with a list of rules, `[{"match": "...", "answer": "..."}]`. A rule with `"tool": {"name": ..., "arguments": {...}}` calls that tool first and answers its result. A file that cannot be read or is not a JSON list of rules stops the command with exit status 1. |
| `--llm-answer` | none | A canned answer, used in turn when no rule matches. Repeatable. |
| `--llm-delay` | `50ms` | Pause before each streamed chunk. `0s` streams at once. |
| `--llm-chunk-words` | `1` | Words per streamed chunk. |
| `--llm-strip-tag` | none | A tag whose `<tag>...</tag>` blocks a client appends to user messages and the model ignores. Repeatable. |

## The banner

Once the listener is bound the command prints a banner to stdout:

```text
tgfake v0.1.0: fake Bot API for @tgfake_bot at http://127.0.0.1:18790
  Bot API:    http://127.0.0.1:18790/bot<token>/<method>  (in place of https://api.telegram.org)
  chat page:  http://127.0.0.1:18790/
  sim API:    http://127.0.0.1:18790/sim/
  model:      http://127.0.0.1:18790/v1  (OpenAI-compatible, model tgfake-demo, any API key)
```

| Line | What it is for |
|------|----------------|
| first line | The version, the bot's username (`--bot-username`) and the origin the stand serves. |
| `Bot API` | The address a bot library takes in place of `https://api.telegram.org`. Most libraries want only the origin. |
| `chat page` | The person's side of the chat in a browser. |
| `sim API` | The root of the [simulation API](sim-api.md) a test drives. |
| `model` | Printed only with `--llm`: the base URL an OpenAI client takes, and the model id. |

The origin is the address the listener actually got. With `--addr 127.0.0.1:0` the line names the port picked, so a script that starts the command on a free port reads the origin from the first line:

```bash
tgfake --addr 127.0.0.1:0 > tgfake.out 2>&1 &
sleep 0.5
origin="$(sed -n '1s/.* at //p' tgfake.out)"
```

An address with no host or with `0.0.0.0`, such as `--addr :18790`, listens on every interface, and the banner shows it as `http://[::]:18790`; a client connects to a real address of the machine instead.

## Version

`--version` prints `tgfake <version>` to stdout and exits with status 0. The version is, in this order:

1. the value linked into the binary with `-ldflags "-X main.version=..."`: release archives carry the tag, for example `v0.1.0`, and `make build` links the output of `git describe --tags --always --dirty`;
2. the module version Go recorded in the binary: `go install github.com/EvilFreelancer/tgfake/cmd/tgfake@v0.1.0` records `v0.1.0`, and a `go build` inside a git checkout records a pseudo-version with recent Go toolchains;
3. `dev`, which is what `go run ./cmd/tgfake` reports.

## Stopping

SIGINT (Ctrl-C) and SIGTERM stop the command. It first releases every `getUpdates` request held open, so a polling bot gets its answer (an empty batch) at once instead of after `--poll-max`, and then shuts the HTTP server down, waiting up to 5 seconds for the requests in flight to finish. A stop that completes in time is a clean stop.

## Exit status

| Status | When |
|--------|------|
| 0 | A clean stop after SIGINT or SIGTERM; `--version`; `-h` or `--help`, which print the usage to stderr. |
| 1 | An error: an unknown flag or a bad flag value (the usage and the error go to stderr), an unreadable or invalid `--llm-script`, an address that cannot be listened on such as a busy port, or requests still open 5 seconds into a stop (for example a slow streamed model answer), reported as `context deadline exceeded`. |

Errors are printed to stderr prefixed with `tgfake:`:

```text
tgfake: listen tcp 127.0.0.1:18790: bind: address already in use
```
