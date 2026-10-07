# Using tgfake from Go

The tgfake server is also a set of Go packages, so a bot written in Go can be tested with the stand running in-process on `httptest`, with no binary to install and no port to pick. This page covers the four packages: [`pkg/server`](../pkg/server), the fake Bot API with the person's side as methods; [`pkg/botapi`](../pkg/botapi), the wire types; [`pkg/webapp`](../pkg/webapp), the Mini App launch helpers; and [`pkg/llmstub`](../pkg/llmstub), the scripted model. `go doc` on each package has the full signatures.

```bash
go get github.com/EvilFreelancer/tgfake@latest
```

The module needs Go 1.22 or newer (`go 1.22.0` in `go.mod`). The packages import nothing but the standard library; the one requirement in `go.mod`, `github.com/cucumber/godog`, is used by the repository's own tests and is not compiled into yours.

## pkg/server

### Creating and serving

`server.New(server.Options{})` returns an empty stand. `Handler()` serves the Bot API under `/bot<token>/<method>`, the [simulation API](sim-api.md) under `/sim/` and the chat page at `/`. Every method of `Server` is safe for concurrent use.

| `Options` field | Default | Meaning |
|-----------------|---------|---------|
| `Token` | empty | The only token accepted in `/bot<token>/` paths, and the token launch data is signed with. Empty accepts any. |
| `BotUsername` | `server.DefaultBotUsername` (`tgfake_bot`) | What `getMe` reports. |
| `BotFirstName` | `server.DefaultBotFirstName` (`tgfake`) | The bot's first name in `getMe` and in its messages. |
| `BotID` | `server.DefaultBotID` (`7000000001`) | The user id `getMe` reports. |
| `MaxPollWait` | 30 s | The longest a `getUpdates` request is held open. Keep it short in tests. |
| `AllowedUpdates` | nil | The subscription the bot starts with, as a previous bot process would have left it; nil delivers every kind. |
| `Logf` | nil | Receives one line per Bot API call, for example `t.Logf`. |

`Close()` releases every `getUpdates` request held open. Call it before `httptest.Server.Close`, which waits for open requests to finish and would otherwise wait for a held poll to time out. After `Close` a poll answers at once instead of waiting; calling `Close` more than once is harmless.

```go
fake := server.New(server.Options{MaxPollWait: 200 * time.Millisecond, Logf: t.Logf})
srv := httptest.NewServer(fake.Handler())
t.Cleanup(func() {
	fake.Close() // release held polls first
	srv.Close()
})
// The bot under test takes srv.URL in place of https://api.telegram.org.
```

### The person's side

| Method | What it does |
|--------|--------------|
| `InjectMessage(in IncomingMessage) (updateID, messageID int)` | The person sends a message; it is stored in the chat and queued as the next update. |
| `InjectCallback(in IncomingCallback) (updateID int, callbackID string, err error)` | The person taps a button, by `Label` or by `Data`; an error says why nothing could be tapped. |
| `LaunchWebApp(req WebAppLaunch) (LaunchedWebApp, error)` | The person opens a Mini App; the answer is the launch address and the signed launch data. |

`IncomingMessage`, `IncomingCallback` and `WebAppLaunch` are the request bodies of `POST /sim/message`, `POST /sim/callback` and `POST /sim/webapp/launch`, with the same fields and defaults: zero fields mean user 4242 `alice` in her private chat 4242. The fields are listed in [sim-api.md](sim-api.md#post-simmessage) and [mini-apps.md](mini-apps.md#launches). The default user's id is not exported; tests write `4242`.

### Reading what the bot did

| Method | What it returns |
|--------|-----------------|
| `Chat(id int64) ChatView` | The transcript of a chat; an unknown chat is an empty one. |
| `Chats() []ChatSummary` | The chats the stand knows, by id. |
| `Calls(method string) []Call` | The outbox, oldest first, of one method (compared without regard to case) or of every method for `""`. |
| `WaitCall(method string, n int, timeout time.Duration) bool` | Waits until the outbox holds at least n calls of the method, checking every 10 ms; false when the timeout passed first. |
| `Commands() []botapi.BotCommand` | What the bot last set with `setMyCommands`. |
| `MenuButton(chat int64) botapi.MenuButton` | The menu button a chat shows; chat 0 is the bot's own. |
| `File(id string) ([]byte, bool)` | A copy of the bytes the bot uploaded under a `file_id`. |
| `PendingUpdates() int` | Updates queued and not yet confirmed by a poll. |
| `AllowedUpdates() []string` | The subscription in force; nil means every kind. |
| `SetAllowedUpdates(kinds []string)` | Sets the subscription as a previous bot process would have left it; nil means every kind. |
| `Token() string` | The token launch data is signed with: `Options.Token`, else the one of the latest Bot API call. |
| `BotUsername() string` | The username `getMe` reports. |

The outbox records every call when the stand answers it, refused ones included, so `WaitCall("sendMessage", 1, ...)` also returns true for a `sendMessage` the stand refused, and a `getUpdates` held open is counted only once it returns. Check `Call.Status`, or the transcript, when that matters.

`ChatView` is the JSON of `GET /sim/chat/{id}` ([sim-api.md](sim-api.md#get-simchatid)): `ChatID`, `Type`, `Title`, `Typing`, `Messages` (`[]MessageView`), `Drafts` (`[]DraftView`), `Callbacks` (`[]CallbackAnswer`) and `MenuButton`. Two methods read it the way a person would:

- `FindButton(label string) (messageID int, data string, ok bool)` finds a button by its visible text: the newest message that is not deleted and shows it wins, and a `✓ ` prefix is ignored on both sides. It returns the message and the button's `callback_data`.
- `Text() string` renders the transcript as plain lines, the text format of [sim-api.md](sim-api.md#the-text-format).

A `MessageView` has `MessageID`, `From` (`"bot"` or `"user"`), `Username`, `Text`, `Caption`, `Photo` and `Document` (`*FileView`), `ParseMode`, `ReplyToMessageID`, `Edited`, `Deleted`, `Rich` and `Keyboard`. A `Call` has `Seq`, `At`, `Method`, `Params` (the first value of each parameter), `Status` and `Response` (the raw JSON answered).

### Faults

| Method | What it does |
|--------|--------------|
| `SetFault(f Fault)` | Schedules a fault; one per method, the newest replacing the older. An empty `Method` means `"*"`, a zero `Code` 500, an empty `Description` the one derived from the code. |
| `ClearFault(method string)` | Removes the fault of a method; `""` or `"*"` removes the catch-all. |
| `ClearFaults()` | Removes every fault. |
| `Faults() []Fault` | The faults still scheduled, in no particular order. |

`Fault` has `Method`, `Code`, `Description`, `RetryAfter`, `Times` and `Contains`, which behave as the fields of `POST /sim/fault` ([sim-api.md](sim-api.md#faults)).

```go
fake.SetFault(server.Fault{Method: "sendMessage", Code: 429, RetryAfter: 1, Times: 1})
```

### Reset

`Reset()` forgets the chats, the outbox, the faults, the commands, the callback queries, the menu buttons and the uploaded files, like `POST /sim/reset`. Update ids keep growing and the `allowed_updates` subscription stays.

### Exported names

The constants `DefaultBotUsername`, `DefaultBotFirstName` and `DefaultBotID` are the bot a stand with no options plays. The types are `Server`, `Options`, `Call`, `CallbackAnswer`, `ChatSummary`, `ChatView`, `DraftView`, `Fault`, `FileView`, `IncomingCallback`, `IncomingMessage`, `LaunchedWebApp`, `MessageView` and `WebAppLaunch`.

### A complete test

[`examples/echobot`](../examples/echobot) is a bot written with the standard library: `Bot{API, Token, PollTimeout, Client}` long-polls `getUpdates` at `API`, echoes a message as a reply, answers `/start` with a keyboard and edits that message when its Ping button is tapped. Its test runs the bot's own polling loop against the stand in-process and plays the person through the Go API:

```go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/server"
)

// startBot runs the bot under test against a stand of its own and returns the
// stand. The cleanup stops the bot, then releases held polls, then closes the
// listener.
func startBot(t *testing.T) *server.Server {
	t.Helper()
	fake := server.New(server.Options{MaxPollWait: 200 * time.Millisecond})
	srv := httptest.NewServer(fake.Handler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		bot := &Bot{API: srv.URL, Token: "123:TEST", PollTimeout: time.Second, Client: &http.Client{}}
		_ = bot.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		fake.Close()
		srv.Close()
	})
	return fake
}

func TestEchoesAMessageAsAReply(t *testing.T) {
	fake := startBot(t)
	_, msgID := fake.InjectMessage(server.IncomingMessage{Text: "hello"})
	if !fake.WaitCall("sendMessage", 1, 5*time.Second) {
		t.Fatalf("the bot did not answer:\n%s", fake.Chat(4242).Text())
	}
	msgs := fake.Chat(4242).Messages
	last := msgs[len(msgs)-1]
	if last.From != "bot" || last.Text != "You said: hello" || last.ReplyToMessageID != msgID {
		t.Fatalf("the bot answered %+v", last)
	}
}

func TestATapOnPingEditsTheMessage(t *testing.T) {
	fake := startBot(t)
	fake.InjectMessage(server.IncomingMessage{Text: "/start"})
	if !fake.WaitCall("sendMessage", 1, 5*time.Second) {
		t.Fatal("no greeting")
	}
	if _, _, err := fake.InjectCallback(server.IncomingCallback{Label: "Ping"}); err != nil {
		t.Fatalf("tap: %v\n%s", err, fake.Chat(4242).Text())
	}
	if !fake.WaitCall("editMessageText", 1, 5*time.Second) {
		t.Fatalf("the tap was not handled:\n%s", fake.Chat(4242).Text())
	}
	chat := fake.Chat(4242)
	if !strings.Contains(chat.Text(), "Pong.") {
		t.Fatalf("chat:\n%s", chat.Text())
	}
	if len(chat.Callbacks) != 1 || chat.Callbacks[0].Text != "Pong!" {
		t.Fatalf("the tap was answered %+v", chat.Callbacks)
	}
}

func TestTheBotMeetsAScheduledFlood(t *testing.T) {
	fake := startBot(t)
	fake.SetFault(server.Fault{Method: "sendMessage", Code: 429, RetryAfter: 1, Times: 1})
	fake.InjectMessage(server.IncomingMessage{Text: "hello"})
	if !fake.WaitCall("sendMessage", 1, 5*time.Second) {
		t.Fatal("the bot never tried to answer")
	}
	if got := fake.Calls("sendMessage")[0].Status; got != http.StatusTooManyRequests {
		t.Fatalf("first sendMessage answered %d, want the scheduled 429", got)
	}
}
```

The order of the cleanup matters: the bot stops polling first, `fake.Close` then releases any poll still held, and only then does `srv.Close` wait for the open connections.

### The scripted model on the same server

A bot that talks to a language model as well as to Telegram can find both on one `httptest` server, mounted the way the command mounts them ([`newMux`](../cmd/tgfake/main.go)): the model under `/v1/`, the stand under everything else.

```go
fake := server.New(server.Options{MaxPollWait: 200 * time.Millisecond})
model := &llmstub.Server{
	Rules: []llmstub.Rule{{Match: "weather", Answer: "It is sunny."}},
}
mux := http.NewServeMux()
mux.Handle("/v1/", model.Handler())
mux.Handle("/", fake.Handler())
srv := httptest.NewServer(mux)
t.Cleanup(func() {
	fake.Close()
	srv.Close()
})
// Bot API origin: srv.URL; OpenAI base URL: srv.URL + "/v1", any API key.
```

## pkg/botapi

The wire shapes of the Bot API objects the stand sends and reads, written for the stand rather than borrowed from a Telegram library: `Update`, `Message`, `User`, `Chat`, `MessageEntity`, `CallbackQuery`, `InlineKeyboardMarkup`, `InlineKeyboardButton`, `WebAppInfo`, `MenuButton`, `PhotoSize`, `Document` and `BotCommand`. They carry the subset of fields the stand produces. A test decodes what the stand answered with them, for example the message a call returned:

```go
call := fake.Calls("sendMessage")[0]
var answer struct {
	OK     bool           `json:"ok"`
	Result botapi.Message `json:"result"`
}
if err := json.Unmarshal(call.Response, &answer); err != nil {
	t.Fatal(err)
}
if kb := answer.Result.ReplyMarkup; kb == nil || kb.InlineKeyboard[0][0].CallbackData != "ping" {
	t.Fatalf("keyboard: %+v", kb)
}
```

## pkg/webapp

What Telegram does around a Mini App launch, with no state of its own ([mini-apps.md](mini-apps.md)):

| Name | What it is |
|------|------------|
| `Sign(token string, data url.Values) string` | The `hash` Telegram puts into launch data for these fields under the bot's token. |
| `Validate(initData, token string) (url.Values, error)` | Checks launch data against the token and returns its fields; `ErrBadHash` when the hash is missing or wrong. It does not judge `auth_date`. |
| `ErrBadHash` | The error `Validate` returns for a hash that does not check out. |
| `ThemeParams(scheme string) map[string]string` | The colours of the `light` theme, or of the dark one for any other scheme. |
| `URLProblem(raw string) string` | Why Telegram would not open an address as a Mini App, or `""`: an absolute https address, or plain http on this machine. |
| `IsLoopbackHost(host string) bool` | Whether a host is `localhost` or a loopback address. |
| `AppendLaunchParams(app, params string) string` | Puts encoded launch parameters into the fragment of an address. |
| `Version`, `Platform`, `DefaultColorScheme` | `"10.1"`, `"tgfake"` and `"dark"`: what a launch reports by default. |

Launch data of your own, signed the way the stand signs it:

```go
data := url.Values{}
data.Set("query_id", "test-1")
data.Set("user", `{"id":4242,"first_name":"Alice","username":"alice"}`)
data.Set("auth_date", strconv.FormatInt(time.Now().Unix(), 10))
data.Set("hash", webapp.Sign(botToken, data))
initData := data.Encode()

fields, err := webapp.Validate(initData, botToken) // err == nil
```

## pkg/llmstub

The scripted model of [scripted-model.md](scripted-model.md). A `*llmstub.Server` serves `GET /v1/models` and `POST /v1/chat/completions` from `Handler()`. Set its fields before it serves:

| Field | Zero value | Meaning |
|-------|------------|---------|
| `Model` | `llmstub.DefaultModel` (`tgfake-demo`) | The model id reported and echoed. |
| `Rules` | none | `[]llmstub.Rule`, checked first, in order: `Match`, `Answer` and an optional `Tool *llmstub.ToolCall` with `Name` and `Arguments` (a `json.RawMessage` holding a JSON object). |
| `Answers` | none | Canned answers used in turn when no rule matches. |
| `Delay` | no pause | The pause before each streamed chunk. The command's 50 ms is a flag default; in Go the zero value streams at once. |
| `ChunkWords` | 1 | Words per streamed chunk. |
| `StripTags` | none | Tags whose `<tag>...</tag>` blocks are cut out of user messages before the prompt is chosen. |

`Calls()` reports how many completions the model has answered. `Answer(prompt)` picks the answer to a prompt as a completion would and counts it as a call, so it moves the answers in turn on too.

```go
model := &llmstub.Server{
	Rules: []llmstub.Rule{{
		Match:  "weather",
		Tool:   &llmstub.ToolCall{Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`)},
		Answer: "It is sunny in Paris.",
	}},
	StripTags: []string{"turn_context"},
}
```
