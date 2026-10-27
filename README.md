# tgfake

**tgfake server** is a fake Telegram Bot API for testing bots offline. It
answers the Bot API methods a bot calls, keeps the chats the bot writes to,
and lets a test, a CI job or a person at a browser play the user: send
messages, tap reply and inline buttons, open Mini Apps, and make Telegram fail on
purpose. No token, no phone, no network.

![The chat page: the person's side of the conversation on the left, the bot's keyboard as buttons, every Bot API call the bot made on the right](docs/assets/chat-page.png)

- **A real Bot API surface.** `getMe`, long-polled `getUpdates` with
  `offset` and `allowed_updates`, `sendMessage`, `editMessageText`,
  `answerCallbackQuery`, file downloads, photos/video/audio/voice and albums,
  copy/forward operations, inline results, webhooks, chat information, bot
  commands per scope and language, invoices with the pre-checkout query and
  the payment service message, the menu button, and the Bot API 10.1 rich
  messages and drafts with the 10.3 Stop button. Forms, JSON and multipart
  are all accepted, so any Bot API library works unchanged.
- **Strict where Telegram is strict.** An edit that changes nothing, a reply
  to a message that is not there, an answer to an unknown callback query, a
  second answer to the same one, `callback_data` over 64 bytes, a `null`
  keyboard: each is refused with Telegram's own error, so the bug shows up in
  the test and not in a user's chat.
- **The person's side, scriptable.** A [simulation API](docs/sim-api.md)
  under `/sim/` injects text, media, inline queries, Mini App data and taps,
  selects inline results, configures chats and members, returns the transcript and every
  Bot API call, schedules chat-scoped HTTP errors, delays and lost responses for the Bot API
  and file downloads, and opens
  Mini Apps with launch data signed by the bot's token.
- **A chat page.** `http://127.0.0.1:18790/` is a Telegram client for the
  bot: type messages, tap buttons, open Mini Apps in a phone frame and watch
  the Bot API calls arrive. With `--inline`, an Inline button lets you search
  and select the bot's results. Links open in Telegram's
  [in-app browser](docs/in-app-browser.md) on a phone-sized screen, so a site
  can be checked at the width a Telegram user sees it.
- **A scripted model.** `--llm` also serves an OpenAI-compatible
  `/v1/chat/completions` that answers by rule, in turn or by echo, streams
  word by word and can call tools, so an LLM bot runs with no key. Named
  sequences isolate clients, match model/system/tools, and script provider
  errors and timeout recovery with a request journal. The self-contained
  [dialog bot example](examples/dialogbot) exercises a guarded tool dialog.
- **Local Telegram Serverless.** `--serverless <project>` runs `tgcloud/`
  JavaScript handlers and authenticated Mini App endpoints in Docker, with
  the platform SDK and a shared SQLite database. Node.js stays in the
  container; [serverless.md](docs/serverless.md) covers the runtime and its limits.
- **Three ways in.** A single static binary for Linux, macOS and Windows, a
  container image, or a Go package to run in-process on `httptest`.

## Install

Download a release archive from the
[releases page](https://github.com/EvilFreelancer/tgfake/releases), or let
the install script pick the one for your system and check its checksum:

```bash
curl -sSfL https://raw.githubusercontent.com/EvilFreelancer/tgfake/main/scripts/install.sh | sh -s -- -b ~/.local/bin
```

With Go 1.22 or newer:

```bash
go install github.com/EvilFreelancer/tgfake/cmd/tgfake@latest
```

In GitHub Actions, the [action](docs/ci.md) installs it and can start it:

```yaml
- uses: EvilFreelancer/tgfake@v1.3.1   # installs the v1.3.1 binary
  with:
    start: "true"
    args: --llm
```

Releases from v1.2.0 on also publish a container image for Linux amd64 and
arm64. It runs as a non-root user and listens on `0.0.0.0:18790`; flags
after the image name are added to that:

```bash
docker run --rm -p 127.0.0.1:18790:18790 ghcr.io/evilfreelancer/tgfake:1.3.1 --llm
```

## Quick start

Start the stand:

```bash
tgfake --llm
```

```text
tgfake v1.3.1: fake Bot API for @tgfake_bot at http://127.0.0.1:18790
  Bot API:    http://127.0.0.1:18790/bot<token>/<method>  (in place of https://api.telegram.org)
  chat page:  http://127.0.0.1:18790/
  sim API:    http://127.0.0.1:18790/sim/
  model:      http://127.0.0.1:18790/v1  (OpenAI-compatible, model tgfake-demo, any API key)
```

Point the bot at `http://127.0.0.1:18790` instead of
`https://api.telegram.org`. Any token is accepted unless `--token` names one.
Most libraries take the address as an option:

| Library | Setting |
|---------|---------|
| python-telegram-bot | `Application.builder().token(TOKEN).base_url("http://127.0.0.1:18790/bot")` |
| aiogram 3 | `Bot(TOKEN, session=AiohttpSession(api=TelegramAPIServer.from_base("http://127.0.0.1:18790")))` |
| pyTelegramBotAPI | `telebot.apihelper.API_URL = "http://127.0.0.1:18790/bot{0}/{1}"` |
| Telegraf | `new Telegraf(TOKEN, { telegram: { apiRoot: "http://127.0.0.1:18790" } })` |
| grammY | `new Bot(TOKEN, { client: { apiRoot: "http://127.0.0.1:18790" } })` |
| node-telegram-bot-api | `new TelegramBot(TOKEN, { polling: true, baseApiUrl: "http://127.0.0.1:18790" })` |
| go-telegram-bot-api v5 | `tgbotapi.NewBotAPIWithAPIEndpoint(TOKEN, "http://127.0.0.1:18790/bot%s/%s")` |
| go-telegram/bot | `bot.New(TOKEN, bot.WithServerURL("http://127.0.0.1:18790"))` |

Then open the chat page and talk to the bot, or drive it from a script. With
the [example bot](examples/echobot) running against the stand
(`TELEGRAM_API=http://127.0.0.1:18790 BOT_TOKEN=123:fake go run ./examples/echobot`):

```bash
curl -s -X POST http://127.0.0.1:18790/sim/message -d '{"text": "hello"}'
curl -s "http://127.0.0.1:18790/sim/chat/4242?format=text"
```

```text
[1] alice: hello
[2] bot: You said: hello
```

## Testing a bot

**Any language.** Start the binary and the bot as two processes and check
the chat through the simulation API. [`examples/shell/bot-e2e.sh`](examples/shell/bot-e2e.sh)
is a complete test of that shape: it sends a message, waits for the answer,
taps a button and injects a flood-control fault. Set `BOT_CMD` to run it
against your own bot.

**Go.** Run the server in-process and drive it through its Go API:

```go
import "github.com/EvilFreelancer/tgfake/pkg/server"

fake := server.New(server.Options{})
srv := httptest.NewServer(fake.Handler())
defer func() { fake.Close(); srv.Close() }() // release long polls first

go runBot(ctx, srv.URL) // the bot under test, its API origin set to srv.URL

fake.InjectMessage(server.IncomingMessage{Text: "hello"})
fake.WaitCall("sendMessage", 1, 5*time.Second)
fmt.Print(fake.Chat(4242).Text()) // [1] alice: hello / [2] bot: ...
```

[`examples/echobot`](examples/echobot) is a bot written with the standard
library and its tests; [docs/go.md](docs/go.md) covers the whole Go API.

**CI.** The action installs a release on Linux, macOS and Windows runners and
can start it in the background; [docs/ci.md](docs/ci.md) has workflows for
GitHub Actions and the install script for everything else.

**Docker Compose.** A bot in its own container reaches the stand by its
service name, `http://tgfake:18790`. A webhook bot registers an address such
as `http://bot:8080/webhook`, which Telegram's HTTPS rules refuse, so the
stand is started with `--allow-http-webhook` to accept it.
[docs/docker.md](docs/docker.md) has a working example with an echo bot.

## Documentation

| Page | What it covers |
|------|----------------|
| [docs/command.md](docs/command.md) | Every flag of the `tgfake` command, the banner, signals and exit codes. |
| [docs/bot-api.md](docs/bot-api.md) | The Bot API methods served, what each refuses the way Telegram does, and what is not implemented. |
| [docs/sim-api.md](docs/sim-api.md) | The simulation API: messages, taps, transcripts, the outbox, faults, Mini App launches, reset. |
| [docs/mini-apps.md](docs/mini-apps.md) | `web_app` buttons, the menu button, signed launch data and the phone frame of the chat page. |
| [docs/in-app-browser.md](docs/in-app-browser.md) | Links in the chat page opening in Telegram's in-app browser: screen sizes, the bars, what the frame does not reproduce, hooks for browser tests. |
| [docs/scripted-model.md](docs/scripted-model.md) | The OpenAI-compatible model of `--llm`: answers, rules, tool calls, streaming. |
| [docs/serverless.md](docs/serverless.md) | Local tgcloud projects in Docker: handlers, SDK, SQLite, authenticated endpoints and Go tests. |
| [docs/go.md](docs/go.md) | Using the packages from Go tests: `server`, `botapi`, `webapp`, `llmstub`. |
| [docs/ci.md](docs/ci.md) | The GitHub Action, the install script and running the stand in other CI systems. |
| [docs/docker.md](docs/docker.md) | The container image, a Docker Compose example with a webhook bot and `--allow-http-webhook`. |
| [docs/development.md](docs/development.md) | Building, testing, the layout of the repository and cutting a release. |

## History

tgfake started as the offline Telegram stand of
[Coddy](https://github.com/coddy-project/coddy-agent), whose Telegram gateway
is tested against it, and moved into this repository with its history.

## License

[MIT](LICENSE)
