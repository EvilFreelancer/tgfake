# The scripted model

`tgfake --llm` also serves a scripted model in the shape of the OpenAI Chat Completions API, so a bot built on a language model runs on the stand with no key and no network. This page covers its routes, how it picks an answer, the rules file with tool calls, the stripping of context a client appends, and the streaming pace. The code is the package [`pkg/llmstub`](../pkg/llmstub/llmstub.go); the flags are listed in [command.md](command.md#scripted-model-flags), and mounting it from Go is in [go.md](go.md#pkgllmstub).

## Pointing a client at it

The model is served on the same origin as the Bot API, under `/v1`. An OpenAI client takes `<origin>/v1` as its base URL, for example `http://127.0.0.1:18790/v1`. Any API key is accepted, because the `Authorization` header is not read, and so is any model name in a request. The model id it reports is `tgfake-demo` unless `--llm-model` names another.

## Routes

| Route | Answer |
|-------|--------|
| `GET /v1/models` | A list with the one model. |
| `POST /v1/chat/completions` | A completion, streamed when the request says `"stream": true`. |

Any other path under `/v1/` is answered with a plain-text `404 page not found`. Without `--llm` the command does not serve `/v1` at all, and such a path gets the Bot API's 404 envelope.

```bash
curl -s http://127.0.0.1:18790/v1/models
```

```json
{"data":[{"id":"tgfake-demo","object":"model","owned_by":"tgfake"}],"object":"list"}
```

Completion requests read `messages`, `stream`, and, for extended rules, `model`, `user` and offered `tools`. `tool_choice` is recorded for diagnostics. `temperature`, `max_tokens`, `stream_options` and other generation options are ignored. `content` is a string or an array of typed parts, of which the `text` parts count. A body that is not JSON is answered `400` with `{"error":{"message":"invalid JSON body"}}`.

### Without streaming

```bash
curl -s http://127.0.0.1:18790/v1/chat/completions \
  -d '{"model": "any", "messages": [{"role": "user", "content": "hi there"}]}'
```

```json
{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"You said: hi there","role":"assistant"}}],"created":1791385558,"id":"chatcmpl-tgfake-1","model":"tgfake-demo","object":"chat.completion","usage":{"completion_tokens":4,"prompt_tokens":1,"total_tokens":5}}
```

The id is `chatcmpl-tgfake-<n>`, where n numbers the completion calls the model has answered. `usage` reports 1 prompt token and the number of words of the answer as completion tokens.

### Streaming

A streamed answer is `text/event-stream`, one `data:` frame per event:

1. a chunk with `delta` `{"role": "assistant", "content": ""}`;
2. one chunk per piece of the answer, each `--llm-chunk-words` words long with the whitespace after them, each sent after a pause of `--llm-delay`;
3. a chunk with an empty `delta` and `finish_reason` `stop`;
4. a chunk with no choices and the `usage`, the way an OpenAI stream ends when usage is asked for (it is sent whatever `stream_options` says);
5. `data: [DONE]`.

```bash
curl -sN http://127.0.0.1:18790/v1/chat/completions \
  -d '{"stream": true, "messages": [{"role": "user", "content": "two words"}]}'
```

```text
data: {"choices":[{"delta":{"content":"","role":"assistant"},"finish_reason":null,"index":0}],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"You "},"finish_reason":null,"index":0}],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"said: "},"finish_reason":null,"index":0}],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"two "},"finish_reason":null,"index":0}],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"words"},"finish_reason":null,"index":0}],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk"}

data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk"}

data: {"choices":[],"created":1791385558,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion.chunk","usage":{"completion_tokens":4,"prompt_tokens":1,"total_tokens":5}}

data: [DONE]
```

## How an answer is chosen

The prompt is the text of the newest `user` message that has any text, trimmed (after [stripping](#stripping-context-a-client-appends) when `--llm-strip-tag` is set). Messages of other roles do not count, so the prompt is the person's last message even when assistant or tool messages follow it. The answer is then picked in this order:

1. **Rules.** The first rule whose `match` occurs in the prompt, compared without regard to case, wins. A rule with an empty `match` matches every prompt, so it belongs at the end of the list as a catch-all.
2. **Answers in turn.** With no rule matching, the canned answers of `--llm-answer` are used in turn: the n-th completion call gets the answer at position `(n - 1) mod <number of answers>`, counting from 0, so they repeat from the first once used up.
3. **Echo.** With neither, the answer is `You said: <prompt>`.

The count n behind the answers in turn is the number of completion calls the model has answered, legacy calls included: a call a legacy rule answered, a tool call and a call made by the client for its own purposes all move it on. Started with `--llm-script rules.json --llm-answer first --llm-answer second`, where the rules match `ping`, four prompts in a row are answered like this:

| Call | Prompt | Answer |
|------|--------|--------|
| 1 | `a` | `first` |
| 2 | `b` | `second` |
| 3 | `ping` | the rule's answer |
| 4 | `c` | `second`, not `first`: call 3 counted |

A client that makes model calls of its own besides the answer to the person, to title a conversation, summarise it or update a memory, moves the canned answers on in the same way, so the person sees them out of order. Rules key on the person's words and do not depend on the number of calls, which makes them the reliable choice for a test.

## The rules file

`--llm-script` names a JSON file with a list of rules ([`llmstub.Rule`](../pkg/llmstub/llmstub.go)):

| Field | Meaning |
|-------|---------|
| `match` | Text to find in the prompt, case-insensitive. Empty matches everything. |
| `answer` | The text the model answers. |
| `tool` | Optional. A tool call the model makes before it answers: `name` and `arguments`, the arguments written as a JSON object, not as an encoded string. |

```json
[
  {"match": "weather", "tool": {"name": "get_weather", "arguments": {"city": "Paris"}}, "answer": "It is sunny in Paris."},
  {"match": "ping", "answer": "pong"},
  {"match": "", "answer": "I only know about the weather."}
]
```

A file that cannot be read, or is not a JSON list of rules, stops the command with exit status 1 and the file's name in the error.

### Tool calls

A rule with a `tool` scripts a whole turn that uses a tool, with no model behind it:

- The first request of a matching turn is answered with the tool call instead of text: a message with `content` `null` and one entry in `tool_calls` (`id` `call_llmstub_<n>`, `type` `function`, `function` with the `name` and the `arguments` as the JSON text from the file, `{}` when there are none), and `finish_reason` `tool_calls`.
- The request that carries the tool's result, recognised by its newest message being a `tool` message, is answered with the rule's `answer` as text. The prompt is still the person's message, so the same rule matches. The result itself is not read: the answer is the one in the file.

Streamed, the tool call arrives as one chunk whose `delta` holds `role` and the complete `tool_calls` entry (with `index` 0), then a chunk with `finish_reason` `tool_calls`, the usage chunk and `[DONE]`, with no pause between them.

```bash
curl -s http://127.0.0.1:18790/v1/chat/completions \
  -d '{"messages": [{"role": "user", "content": "What is the weather?"}]}'
```

```json
{"choices":[{"finish_reason":"tool_calls","index":0,"message":{"content":null,"role":"assistant","tool_calls":[{"function":{"arguments":"{\"city\": \"Paris\"}","name":"get_weather"},"id":"call_llmstub_1","type":"function"}]}}],"created":1791385592,"id":"chatcmpl-tgfake-1","model":"tgfake-demo","object":"chat.completion","usage":{"completion_tokens":1,"prompt_tokens":1,"total_tokens":2}}
```

```bash
curl -s http://127.0.0.1:18790/v1/chat/completions -d '{"messages": [
  {"role": "user", "content": "What is the weather?"},
  {"role": "assistant", "content": null, "tool_calls": [{"id": "call_llmstub_1", "type": "function", "function": {"name": "get_weather", "arguments": "{}"}}]},
  {"role": "tool", "tool_call_id": "call_llmstub_1", "content": "sunny"}
]}'
```

```json
{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"It is sunny in Paris.","role":"assistant"}}],"created":1791385592,"id":"chatcmpl-tgfake-2","model":"tgfake-demo","object":"chat.completion","usage":{"completion_tokens":5,"prompt_tokens":1,"total_tokens":6}}
```

Legacy rules using only `match`/`answer`/`tool` do not look at the `tools` a request offers: the client has to offer a tool of that name for the call to make sense to it.

## Stripping context a client appends

Some clients append their own state to every request, as a block inside the person's message or as one more `user` message after it; Coddy's `<turn_context>` block is one. Matched or echoed as it is, that block would become the prompt. `--llm-strip-tag <tag>` (repeatable; `Server.StripTags` in Go) names such a tag: every `<tag>...</tag>` block is cut out of a user message before the prompt is chosen, a block without its closing tag runs to the end of the message, and a user message with nothing else in it is skipped, both when the prompt is chosen and when the model decides whether the newest message is a tool result. By default nothing is cut.

```bash
tgfake --llm --llm-strip-tag turn_context
```

With that flag, a request whose messages are `{"role": "user", "content": "hello"}` followed by `{"role": "user", "content": "<turn_context>state</turn_context>"}` is answered `You said: hello`. Without it the newest user message is the block, and the answer is `You said: <turn_context>state</turn_context>`.

## Streaming pace

`--llm-delay` (50 ms by default) is the pause before each piece of a streamed answer, and `--llm-chunk-words` (1 by default) the number of words in a piece. A pause of `0s` sends the whole answer at once, which is what CI usually wants; a longer pause gives a bot that edits its message as the answer streams something to do, and with the default pace a 40-word answer takes about two seconds. A stream stops when the client disconnects during a pause. Legacy answers without streaming are never delayed. Sequenced steps may set `delay_ms` independently.

A long stream also delays the command's stop: the command waits up to 5 seconds for open requests when it is told to stop ([command.md](command.md#stopping)).

## Independent named sequences

The same JSON rule list also accepts `name`, optional filters and `steps`.
Rules are checked in order. Every specified filter is combined with AND:

| Filter | Matching |
|--------|----------|
| `match` | Case-insensitive substring of the latest nonempty user text, as before. |
| `model` | Exact request model. |
| `system` | Case-insensitive substring of the concatenated system messages. |
| `tools` | Every listed name must be offered as a function tool. Extra tools are allowed. |
| `has_tools` | Whether the request offers any tools. |
| `profile` | Exact `X-Tgfake-Profile` header, a test profile label. |
| `user` | Exact explicit client key. |

`name` labels diagnostics. Each rule has its own cursor for each pair of
profile and client key. The key is the standard request `user`, or the value
of `--llm-client-header` / `Server.ClientHeader` when configured. Prompts are
never mined for identity. With no key, identical clients share a cursor;
configure `user`, a test header or separate stub instances to isolate them.
Configure fields before serving; use `SetRules` or the HTTP script route for
concurrent reconfiguration. Reserve test headers for nonsecret identifiers.

```json
[
  {"name":"guard","model":"guard","repeat_last":true,
   "steps":[{"answer":"{\"allow\":true}"}]},
  {"name":"agent","model":"agent","system":"assist","tools":["generate_image"],
   "steps":[
     {"answer":"Which style?"},
     {"tool_calls":[{"name":"generate_image","arguments":{"style":"blue"}}]},
     {"tool_name":"generate_image","tool_result":"ready","answer":"Done",
      "usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}
   ]}
]
```

A matched request reserves one step under a mutex, then releases the mutex
before waiting or writing. A guard call never advances the agent sequence.
There is no implicit cycling: exhaustion returns HTTP 409 with an `error`
object. `repeat_last: true` explicitly repeats the final step. Failed request
validation (409) does not consume a step; a reserved timeout or HTTP failure
does, allowing the next request to recover. Legacy canned-answer rotation is
independent of these new sequences; legacy-only scripts keep their behavior.
A filtered rule without `steps` can return `answer` or `tool` on every match;
use a sequence for a tool turn with a final answer.

Each step may return `answer`, one or more `tool_calls`, a `usage` object,
or `omit_usage: true`. Default usage is a synthetic 1 prompt token, 1 completion
token, 2 total. Tool arguments are JSON objects; generated responses encode
them as strings as Chat Completions expects. Tool names must be offered by
the request. IDs are assigned per call unless a tool explicitly sets `id`.
The next step requires results with all outstanding `tool_call_id` values
after the latest assistant message. `tool_name` and `tool_result` further
require an outstanding tool of that exact name and a case-sensitive substring
of its result. An unrelated last `role: tool` message is insufficient.
No tool schemas or semantic quality are evaluated.

## Provider failures and latency

Steps also accept `status` (for example 401, 402, 403, 429 or 503), `headers`
and `delay_ms`. A step with status 400 or above defaults to an error object. `raw_body` sends
exact bytes as a JSON string in the script, even when those bytes are invalid
JSON. `negative: true` is required for `raw_body`, `empty_choices: true` and
tool `raw_arguments` (a string); it also permits an unoffered tool. These
responses are never repaired. `omit_usage` is valid on positive responses too.

```json
[
  {"name":"primary","profile":"primary","model":"provider-a",
   "steps":[
     {"status":429,"headers":{"Retry-After":"3"},"negative":true,
      "raw_body":"{\"error\":{\"message\":\"quota\"}}"},
     {"answer":"recovered","omit_usage":true}
   ]},
  {"name":"backup","profile":"backup","repeat_last":true,
   "steps":[{"answer":"backup result"}]}
]
```

The caller sets `X-Tgfake-Profile: primary` or `backup`; a failure in one
profile does not consume the other's cursor. `repeat_last` makes a one-step
failure persistent; omit it and add a success step for a one-time failure.
A delayed step watches the request context, so a client timeout cancels it
without holding the scenario mutex. Its journal status is 499 with
`cancelled: true` when cancellation is observed; 499 is diagnostic only.

Extended rules support non-streaming requests. A matching streamed request
gets 409 without consuming a step. Legacy text/tool streaming remains
available. Partial SSE failures and deterministic stream faults are future work.

## Journal and reset

`Handler()` also serves these routes; the command mounts them with `--llm`:

| Route | Effect |
|-------|--------|
| `GET /sim/llm/log` | `{entries, count, calls}`; completed entries in request reservation order, total completion counter in `calls`. |
| `DELETE /sim/llm/log` | Clear completed entries without rewinding counters or cursors. |
| `PUT /sim/llm/script` | Replace the JSON rule array, validate explicit negative steps and reset cursors. Preserve journal and counters. |
| `POST /sim/llm/reset` | Clear journal, rewind scenario cursors and legacy answer rotation. Keep completion IDs and counters increasing. |

In Go use `Log()`, `ClearLog()`, `SetRules(rules)` and `Reset()`. Each entry
records `seq`, `at`, profile, client key, model, messages, full offered tools,
`tool_choice`, selected rule and zero-based step, HTTP status, response bytes
as a string and `latency_ms`. Legacy entries use step -1. Request headers,
including Authorization, are not copied into the journal. Test prompts and
tool results themselves are visible. Credential header names (Authorization,
Cookie, and names containing token, secret or API-key variants) are refused
as client selectors. Use nonsecret test identifiers.
Entries appear after completion; an in-flight request can finish after a
journal clear. Reset or replace scripts between quiescent test runs.

With the command, `POST /sim/reset` resets both Telegram and LLM state; Telegram
update IDs still increase. With independently mounted Go handlers, call both
`fake.Reset()` and `model.Reset()` explicitly. No reset changes the script.

Run the complete offline example and its Gherkin regression:

```bash
go run ./examples/dialogbot
go test ./examples/dialogbot
```

[`examples/dialogbot/script.json`](../examples/dialogbot/script.json) is also
a CLI script: `tgfake --llm --llm-script examples/dialogbot/script.json`.
