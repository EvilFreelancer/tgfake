# The simulation API

The simulation API is the person's side of the chat. A test, a CI job or the chat page uses it to send messages and tap buttons as a user would, to read back what the bot did, to make Telegram fail on purpose and to open Mini Apps. It lives under `/sim/` on the same origin as the [Bot API](bot-api.md), and every route is registered in [`pkg/server/sim.go`](../pkg/server/sim.go). A Go test can reach the same operations through the methods of `server.Server` ([go.md](go.md)).

The examples below assume the command is running on its default address:

```bash
O=http://127.0.0.1:18790
```

## Conventions

Request bodies are JSON objects of at most 1 MB; no `Content-Type` header is required. Answers are JSON, except the text transcript and the file download. A request that cannot be served is answered with an HTTP error status and an object naming the reason:

```json
{"error":"text is empty"}
```

A body that is not valid JSON is answered `400` with `invalid JSON body: <decoder error>`. A route called with the wrong HTTP method falls through to the Bot API and is answered with its `404 Not Found` envelope.

## Routes

| Method and path | What it does |
|-----------------|--------------|
| `POST /sim/message` | The person sends a text message. |
| `POST /sim/callback` | The person taps an inline keyboard button. |
| `POST /sim/draft/stop` | The person presses Stop under a streamed draft. |
| `GET /sim/outbox` | Every Bot API call the stand answered, oldest first. |
| `GET /sim/outbox/count` | How many Bot API calls the stand answered. |
| `GET /sim/chat/{id}` | The transcript of a chat, as JSON or as text. |
| `GET /sim/file/{id}` | The bytes of a photo or document the bot uploaded. |
| `GET /sim/chats` | The chats the stand knows. |
| `GET /sim/state` | The bot, the update queue, the subscription, the commands, the menu button and the faults. |
| `POST /sim/webapp/launch` | The person opens a Mini App. |
| `GET /sim/webapp/theme/{scheme}` | The theme parameters of the light or dark theme. |
| `POST /sim/fault` | Schedule a fault, or clear one. |
| `DELETE /sim/fault` | Clear every fault. |
| `POST /sim/reset` | Forget the chats, the outbox, the faults and the rest of the session. |

## POST /sim/message

The person types a message. It is stored in the chat and becomes the next `message` update a `getUpdates` delivers, provided the bot's `allowed_updates` subscription includes messages at that moment ([bot-api.md](bot-api.md#getupdates)). The message is stored in the transcript either way.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `text` | string | required | The text. A missing or blank text is refused with `400 text is empty`. |
| `user_id` | integer | `4242` | The person's user id. |
| `username` | string | `alice` for user 4242, else none | The person's username. |
| `first_name` | string | the username with its first letter in upper case, else `Alice` | The person's first name. |
| `chat_type` | string | `private` | `private`, `group` or `supergroup`. It takes effect when the message creates the chat; an existing chat keeps its type. |
| `chat_id` | integer | the user id for a private chat, `-100000000000 - user_id` for a group | The chat. Telegram numbers a private chat with the user's own id, so the default user's private chat is 4242. |
| `chat_title` | string | `Fake group` for a group | The title of a group chat, set when the chat has none yet. |
| `reply_to_message_id` | integer | none | Makes the message a reply to a message of the chat. An id that is not in the chat is ignored. |
| `mention` | boolean | `false` | Puts the bot's `@username` and a space in front of the text, with a `mention` entity: how a group message addresses the bot. |

A text that starts with a command, such as `/start` or `/help@tgfake_bot`, carries a `bot_command` entity at offset 0, which is what makes it a command for every Bot API library.

The answer is the id of the update and the id of the message in its chat:

```bash
curl -s -X POST $O/sim/message -d '{"text": "hello"}'
```

```json
{"message_id":1,"update_id":1}
```

A message in a group, addressed to the bot:

```bash
curl -s -X POST $O/sim/message -d '{"text": "what time is it?", "chat_type": "group", "mention": true}'
curl -s "$O/sim/chat/-100000004242?format=text"
```

```text
[1] alice: @tgfake_bot what time is it?
```

## POST /sim/callback

The person taps a button of an inline keyboard. The stand mints a callback query id (`cbq-1`, `cbq-2` and so on), which the bot must answer once with `answerCallbackQuery`, and queues a `callback_query` update carrying the tapped message as it is now, `chat_instance` set to the chat id, and the button's `callback_data` as `data`.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `label` | string | none | The visible text of the button. A `✓ ` prefix, which bots put in front of the current choice, is ignored on both sides. |
| `data` | string | none | The `callback_data` to send. With `data` no button is looked up: the tap carries this data for the target message, whether or not one of its buttons has it. |
| `message_id` | integer | the newest message that still has an inline keyboard | The message whose button is tapped. |
| `chat_id` | integer | the user id | The chat. Set it for a group. |
| `user_id` | integer | `4242` | The person tapping. |
| `username` | string | `alice` for user 4242, else none | The person's username. |
| `first_name` | string | `Alice` | The person's first name. |

One of `label` and `data` is required (`400 data or label is required`). A tap that finds nothing to tap is refused with `404` and one of these reasons:

| Error | When |
|-------|------|
| `chat <id> has no messages` | The chat does not exist. |
| `message <id> not found in chat <id>` | `message_id` names a message that is not there or was deleted. |
| `chat <id> has no message with an inline keyboard` | No `message_id` was given and no message of the chat has a keyboard. |
| `message <id> has no button "<label>"` | The target message has no button with that text. |

A label is looked up on the target message only, and should name a callback button: a `url` or `web_app` button found by its label is delivered as a callback query with empty `data`.

```bash
curl -s -X POST $O/sim/callback -d '{"label": "Yes"}'
```

```json
{"callback_query_id":"cbq-1","update_id":2}
```

## POST /sim/draft/stop

The person presses the Stop button the bot put under a draft by streaming it with `can_stop=true`. The stand queues a `stopped_message_generation` update with the chat, the draft's `message_thread_id` when it had one, and its `draft_id`, provided the bot's `allowed_updates` subscription includes that kind at that moment ([bot-api.md](bot-api.md#getupdates)). The draft leaves the chat unless its latest revision set `keep_on_stop=true`; then it stays without the Stop button.

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `draft_id` | integer | the newest live draft of the chat that shows a Stop button | The draft whose Stop button is pressed. |
| `chat_id` | integer | `4242` | The private chat of the draft. |

A press that finds nothing to press is refused with `404` and one of these reasons:

| Error | When |
|-------|------|
| `chat <id> has no draft with a Stop button` | No `draft_id` was given and no live draft of the chat shows the button. |
| `draft <id> not found in chat <id>` | `draft_id` names a draft that is not there or has expired. |
| `draft <id> in chat <id> shows no Stop button` | The draft's latest revision did not set `can_stop`. |

```bash
curl -s -X POST $O/sim/draft/stop -d '{"draft_id": -1}'
```

```json
{"update_id":3}
```

## GET /sim/outbox

Every Bot API call that named a method, refused calls included, oldest first:

| Query parameter | Meaning |
|-----------------|---------|
| `method` | Only calls of this method, compared without regard to case. |
| `since` | Only calls whose `seq` is greater than this. |

Each call carries `seq` (numbered from 1), `at` (when it was answered), `method` (as the bot spelled it), `params` (the first value of every parameter as a string; an upload is recorded as `<file name> (<size> bytes)`), `status` (the HTTP status) and `response` (the JSON the bot received):

```bash
curl -s "$O/sim/outbox?method=sendMessage"
```

```json
{"calls":[{"seq":4,"at":"2026-10-07T18:04:58.289443257+03:00","method":"sendMessage","params":{"chat_id":"4242","text":"You said: hello"},"status":200,"response":{"ok":true,"result":{"message_id":2,"from":{"id":7000000001,"is_bot":true,"first_name":"tgfake","username":"tgfake_bot"},"chat":{"id":4242,"type":"private","first_name":"Alice","username":"alice"},"date":1791385498,"text":"You said: hello"}}}]}
```

A call is recorded when the stand answers it, so a `getUpdates` held open appears only once it returns, after at most `--poll-max`. `seq` starts again at 1 after `POST /sim/reset`, so a reader that follows the outbox with `since` resets its cursor too.

## GET /sim/outbox/count

The number of calls in the outbox, optionally of one `method` (`since` is not read here):

```bash
curl -s "$O/sim/outbox/count?method=sendMessage"
```

```json
{"count":1}
```

## GET /sim/chat/{id}

The transcript of one chat. `{id}` is the chat id, negative for a group; anything that is not an integer is refused with `400 chat id must be an integer`. A chat the stand has never seen is answered as an empty one.

```bash
curl -s $O/sim/chat/4242
```

```json
{"chat_id":4242,"type":"private","typing":false,"messages":[{"message_id":1,"from":"user","username":"alice","text":"hello","edited":false,"deleted":false,"rich":false},{"message_id":2,"from":"bot","text":"Pick one","edited":false,"deleted":false,"rich":false,"keyboard":[[{"text":"Yes","callback_data":"yes"},{"text":"No","callback_data":"no"}]]}],"drafts":[],"callbacks":[{"id":"cbq-1","text":"Done"}],"menu_button":{"type":"commands"}}
```

| Field | Meaning |
|-------|---------|
| `chat_id`, `type`, `title` | The chat; `title` only for a group. |
| `typing` | Whether "typing…" shows: a `sendChatAction` came less than 5 seconds ago. |
| `messages` | Every message in order, deleted ones included. |
| `drafts` | The rich-message drafts still alive, by `draft_id`: `draft_id`, `markdown`, `updated_at`, `revisions`, and `can_stop` and `keep_on_stop` when the latest revision set them. |
| `callbacks` | Every `answerCallbackQuery` for a tap in this chat: `id`, `text`, `show_alert`. |
| `menu_button` | The menu button the chat shows: its own, else the bot's ([mini-apps.md](mini-apps.md#the-menu-button)). |

A message has `message_id`; `from`, which is `bot` or `user`; `username` for the person's messages; `text`; `caption`; `photo` or `document` with `file_id`, `name`, `mime_type`, `size` and, for a photo, `width` and `height`; `parse_mode` as the bot sent it; `reply_to_message_id`; the flags `edited`, `deleted` and `rich`; and `keyboard`, the rows of inline buttons.

### The text format

With `?format=text` the answer is `text/plain`, the same transcript as lines a shell script can grep ([`ChatView.Text`](../pkg/server/chat.go)):

```bash
curl -s "$O/sim/chat/4242?format=text"
```

```text
[1] alice: hello
[2] bot: Pick one
    [Yes] [No]
[3] bot: [photo chart.png 640x480] Weekly numbers
[4] bot: [document report.pdf]
[5] bot: first line
    second line
draft 7 (rev 2): partial answer
typing…
```

- Each message that is not deleted is one line, `[<message_id>] <who>: <text>`, where `<who>` is `bot`, the person's username, or `user` for a person with none.
- A photo is shown as `[photo <file name> <width>x<height>]` and a document as `[document <file name>]`, each followed by its caption when it has one.
- The lines after the first of a multi-line text or caption are indented by four spaces.
- Each row of a message's inline keyboard follows it on a line of its own: four spaces, then `[<button text>]` for each button, separated by spaces.
- After the messages comes one line per live draft, `draft <draft_id> (rev <revisions>): <markdown>`, followed by a line `    [Stop]` when the draft shows a Stop button.
- The last line is `typing…` while the chat shows typing.

## GET /sim/file/{id}

The bytes the bot uploaded with `sendPhoto` or `sendDocument` under a `file_id` such as `file_1`, with the content type detected at upload. An unknown id is answered `404 no such file`.

```bash
curl -s -o photo.png $O/sim/file/file_1
```

## GET /sim/chats

The chats the stand knows, by chat id. `title` is `@<username>` of the person for a private chat the person has written in, the chat's title for a group, and `messages` counts every stored message, deleted ones included:

```json
{"chats":[{"chat_id":-100000004242,"type":"group","title":"Fake group","messages":1},{"chat_id":4242,"type":"private","title":"@alice","messages":8}]}
```

## GET /sim/state

A summary of the stand:

```json
{"allowed_updates":null,"bot":{"can_join_groups":true,"can_read_all_group_messages":false,"first_name":"tgfake","id":7000000001,"is_bot":true,"supports_inline_queries":false,"username":"tgfake_bot"},"calls":3,"commands":[],"faults":[],"menu_button":{"type":"commands"},"next_update_id":2,"pending_updates":0}
```

| Field | Meaning |
|-------|---------|
| `bot` | What `getMe` answers. |
| `pending_updates` | Updates queued and not yet confirmed by a poll's `offset`. |
| `next_update_id` | The id the next update will get. |
| `allowed_updates` | The subscription in force; `null` means every kind. |
| `commands` | What the bot last set with `setMyCommands`, `[]` before that. |
| `menu_button` | The bot's own menu button. |
| `faults` | The faults still scheduled. |
| `calls` | The number of calls in the outbox. |

`GET /sim/state` answers as soon as the server is listening, which makes it the readiness check: the GitHub Action polls it after starting the command.

## POST /sim/webapp/launch

The person opens a Mini App, from a `web_app` button (`url` given) or from the chat's menu button (`url` omitted). The answer is the address the client loads, with the launch parameters in its fragment and launch data signed with the bot's token. The fields, the answer and the errors are described in [mini-apps.md](mini-apps.md#launches).

```bash
curl -s -X POST $O/sim/webapp/launch -d '{"url": "https://app.example.com/", "start_param": "abc"}'
```

## GET /sim/webapp/theme/{scheme}

The theme parameters a Telegram client hands a Mini App for `light` or `dark`, the same map a launch carries ([mini-apps.md](mini-apps.md#theme-parameters)). Any other scheme is refused with `400 scheme is light or dark`.

```bash
curl -s $O/sim/webapp/theme/light
```

## Faults

A fault makes the stand answer a Bot API method with an error, the way Telegram does on a bad request, under flood control or during an outage. Faults are checked after the token and before the method, for any method name, so one can be scheduled for a method the stand does not implement.

### POST /sim/fault

The body is a fault:

| Field | Type | Default | Meaning |
|-------|------|---------|---------|
| `method` | string | `*` | The Bot API method, compared without regard to case, or `*` for every method. |
| `code` | integer | `500` | The HTTP status and the `error_code`. |
| `description` | string | derived from `code` | The error text. |
| `retry_after` | integer | none | Fills `parameters.retry_after`, as a 429 carries it. |
| `times` | integer | `0` | How many calls the fault answers before it clears itself; zero or less keeps it until it is cleared. |
| `contains` | string | none | Narrows the fault to calls one of whose parameter values contains this text. A call that does not match passes and is not counted. |
| `clear` | boolean | `false` | Instead of scheduling, remove the fault of `method` (an empty `method` or `*` removes the catch-all). |

The descriptions derived from `code`:

| `code` | `description` |
|--------|---------------|
| 400 | `Bad Request` |
| 401 | `Unauthorized` |
| 403 | `Forbidden: bot was blocked by the user` |
| 404 | `Not Found` |
| 429 | `Too Many Requests: retry after <retry_after>`, or `Too Many Requests` without `retry_after` |
| 502 | `Bad Gateway` |
| any other | `Internal Server Error` |

One fault is kept per method, the newest replacing the older. A call is matched against the fault of its own method first and the catch-all `*` second, so a method fault whose `contains` does not match lets the catch-all answer. The answer lists the faults still scheduled, in no particular order:

```bash
curl -s -X POST $O/sim/fault -d '{"method": "sendMessage", "code": 429, "retry_after": 3, "times": 1}'
```

```json
{"faults":[{"method":"sendmessage","code":429,"description":"Too Many Requests: retry after 3","retry_after":3,"times":1}],"ok":true}
```

The next `sendMessage` is answered with HTTP 429:

```json
{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}
```

A fault for one kind of content only, here Telegram refusing an HTML entity it cannot parse:

```bash
curl -s -X POST $O/sim/fault -d '{"method": "sendMessage", "code": 400, "description": "Bad Request: can'"'"'t parse entities", "contains": "<b>"}'
```

Removing one fault, then every fault:

```bash
curl -s -X POST $O/sim/fault -d '{"method": "sendMessage", "clear": true}'
curl -s -X DELETE $O/sim/fault
```

`DELETE /sim/fault` answers `{"ok":true}`.

## POST /sim/reset

Starts a new session without restarting the command. It answers `{"ok":true}` and forgets:

- the queued updates;
- the chats, with their messages, drafts, callback answers and typing state;
- the outbox, whose `seq` starts again at 1;
- the faults;
- the commands set with `setMyCommands`;
- the callback queries (an answer to a query minted before the reset is refused, and numbering starts again at `cbq-1`);
- the uploaded files (numbering starts again at `file_1`);
- the menu buttons, the bot's and the chats'.

Update ids keep growing: a bot that is polling remembers the last id it confirmed and would drop anything numbered below it. The `allowed_updates` subscription stays as well, as Telegram keeps it with the token rather than with the chats, and so does the token launch data is signed with.

```bash
curl -s -X POST $O/sim/reset
```

## Recipes

### Wait for the bot's answer

The bot answers asynchronously. Count its calls before the person speaks, then poll the count until it grows. Before the first message, wait until the bot has polled at least once so you know it is running. A poll reaches the outbox when it is answered, so start the stand with a short `--poll-max`, such as `1s` as `bot-e2e.sh` does; with the default 30 s the first poll of an idle bot is recorded only after 30 seconds.

```bash
count() { curl -s "$O/sim/outbox/count?method=$1" | tr -dc '0-9'; }

until [ "$(count getUpdates)" -ge 1 ]; do sleep 0.1; done

before="$(count sendMessage)"
curl -s -X POST $O/sim/message -d '{"text": "hello"}' > /dev/null
for _ in $(seq 1 100); do
  [ "$(count sendMessage)" -gt "$before" ] && break
  sleep 0.1
done
curl -s "$O/sim/chat/4242?format=text"
```

Grepping the text transcript in the same loop works as well, as [`examples/shell/bot-e2e.sh`](../examples/shell/bot-e2e.sh) does.

### Tap a button by its label

```bash
curl -s -X POST $O/sim/message -d '{"text": "/start"}' > /dev/null
# ... wait until the transcript shows the keyboard ...
curl -s -X POST $O/sim/callback -d '{"label": "Ping"}'
curl -s "$O/sim/chat/4242" | grep -o '"callbacks":\[[^]]*\]'
```

The tap targets the newest message with a keyboard; give `message_id` to tap an older one. The `callbacks` of the chat show whether and how the bot answered the query.

### Simulate flood control

Answer the bot's next `sendMessage` with a 429 and check that it retries after `retry_after`:

```bash
curl -s -X POST $O/sim/fault -d '{"method": "sendMessage", "code": 429, "retry_after": 2, "times": 1}' > /dev/null
curl -s -X POST $O/sim/message -d '{"text": "under flood control"}' > /dev/null
sleep 3
curl -s "$O/sim/outbox?method=sendMessage" | grep -o '"status":[0-9]*'
```

A bot that retries shows a 429 followed by a 200; one that gives up shows the 429 alone and the transcript lacks the answer.

### Simulate an outage

Make every method fail, including `getUpdates`, then bring Telegram back:

```bash
curl -s -X POST $O/sim/fault -d '{"method": "*", "code": 502}' > /dev/null
sleep 5
curl -s -X DELETE $O/sim/fault > /dev/null
curl -s -X POST $O/sim/message -d '{"text": "are you back?"}' > /dev/null
```

The outbox shows how often the bot polled during the outage, which is how its back-off can be checked, and the transcript shows whether it recovered. A fault with `times` ends an outage on its own after that many calls.
