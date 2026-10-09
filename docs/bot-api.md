# The Bot API surface

This page describes the part of the tgfake server a bot talks to: the Bot API methods it serves, how it reads their parameters, what it answers, the refusals it shares with Telegram and what it does not implement. The code is in [`pkg/server`](../pkg/server): [`methods.go`](../pkg/server/methods.go), [`updates.go`](../pkg/server/updates.go), [`chat.go`](../pkg/server/chat.go), [`faults.go`](../pkg/server/faults.go), [`webapp.go`](../pkg/server/webapp.go) and [`response.go`](../pkg/server/response.go). The person's side of the same chats is the [simulation API](sim-api.md).

## Addressing

A bot calls `<origin>/bot<token>/<method>` where it would call `https://api.telegram.org/bot<token>/<method>`. Any HTTP method works. The method name is matched without regard to case (`getMe`, `getme` and `GETME` are the same method) and a trailing slash after it is ignored.

Any token is accepted unless the command was started with `--token` (or a Go test set `Options.Token`); then every other token is answered `401 Unauthorized`. Without a fixed token the stand remembers the token of the latest call, which is the token Mini App launch data is signed with ([mini-apps.md](mini-apps.md)).

The chat page at `/` and the routes under `/sim/` are served by the stand itself. Every other path that is not of the form `/bot<token>/<method>`, a request to a `/sim/` path with the wrong HTTP method included, is answered with the Bot API's own 404:

```json
{"ok":false,"error_code":404,"description":"Not Found"}
```

## Parameters

Parameters are read the way the Bot API reads them, so any library works unchanged ([`parseParams`](../pkg/server/methods.go)):

| Request | What is read |
|---------|--------------|
| `Content-Type: multipart/form-data` | The query string and the form fields. File parts stay on the request for `sendPhoto` and `sendDocument`. |
| `Content-Type: application/json` | The query string and the top-level keys of a JSON object (up to 4 MB). A string value is taken as is; any other value (a number, a boolean, an object, an array, `null`) is kept as its JSON text, so `"reply_markup": {...}` and `"reply_markup": "{...}"` mean the same. |
| anything else | The query string and an `application/x-www-form-urlencoded` body. |

A parameter whose value is a Bot API object (`reply_markup`, `reply_parameters`, `allowed_updates`, `commands`, `menu_button`, `rich_message`) is JSON text, as on Telegram. A parameter the stand does not read is accepted, recorded in the outbox with the call, and has no other effect.

`chat_id` must be a non-zero integer. A `@channelusername` in its place is answered `Bad Request: chat_id is empty`.

## Responses

Every answer is JSON in the Bot API envelope. A success is HTTP 200:

```json
{"ok":true,"result":{"can_join_groups":true,"can_read_all_group_messages":false,"first_name":"tgfake","id":7000000001,"is_bot":true,"supports_inline_queries":false,"username":"tgfake_bot"}}
```

An error carries its HTTP status as `error_code`, and `parameters.retry_after` when a [fault](sim-api.md#faults) sets one:

```json
{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}
```

A call is checked in this order: the token (401), then a fault scheduled through the simulation API, then the method itself. A fault therefore answers any method, one the stand does not implement included. Every call that names a method, refused or not, is recorded in the outbox (`GET /sim/outbox`) with its parameters, status and response.

## Methods

| Method | Reads | Returns |
|--------|-------|---------|
| `getMe` | nothing | The bot's `User`: `id` 7000000001, `is_bot`, `first_name` (`--bot-name`), `username` (`--bot-username`), `can_join_groups` true, `can_read_all_group_messages` false, `supports_inline_queries` false. |
| `getUpdates` | `offset`, `limit`, `timeout`, `allowed_updates` | An array of `Update`; see [getUpdates](#getupdates). |
| `setMyCommands` | `commands` | `true`. One list is kept; `scope` and `language_code` are not read. |
| `getMyCommands` | nothing | The list last set, `[]` before any `setMyCommands`. |
| `sendMessage` | `chat_id`, `text`, `parse_mode`, `reply_markup`, `reply_parameters` or `reply_to_message_id` and `allow_sending_without_reply` | The sent `Message`. |
| `sendPhoto` | `chat_id`, `photo` (a multipart upload), `caption`, `parse_mode`, `reply_markup`, the reply parameters | The sent `Message` with one `PhotoSize`. |
| `sendDocument` | `chat_id`, `document` (a multipart upload), `caption`, `parse_mode`, `reply_markup`, the reply parameters | The sent `Message` with a `Document`. |
| `editMessageText` | `chat_id`, `message_id`, `text`, `parse_mode`, `reply_markup` | The edited `Message`, with `edit_date`. |
| `editMessageReplyMarkup` | `chat_id`, `message_id`, `reply_markup` | The edited `Message`. Without `reply_markup` the keyboard is removed. |
| `deleteMessage` | `chat_id`, `message_id` | `true`. |
| `sendChatAction` | `chat_id` (`action` is recorded, not read) | `true`. The chat shows "typing…" for 5 seconds. |
| `answerCallbackQuery` | `callback_query_id`, `text`, `show_alert` | `true`. The answer is kept in the chat's `callbacks`. |
| `sendRichMessage` | `chat_id`, `rich_message` (`{"markdown": ...}` or `{"html": ...}`), `reply_markup`, the reply parameters | The sent `Message`; its `text` is the markdown, else the HTML. |
| `sendRichMessageDraft` | `chat_id`, `draft_id`, `rich_message` | `true`. |
| `setChatMenuButton` | `chat_id` (optional), `menu_button` | `true`; see [mini-apps.md](mini-apps.md#the-menu-button). |
| `getChatMenuButton` | `chat_id` (optional) | The `MenuButton` the chat shows. |
| `deleteWebhook` | nothing | `true`. There is no webhook to delete; it is accepted so a library that calls it before polling works. |
| `getWebhookInfo` | nothing | `{"url": "", "has_custom_certificate": false, "pending_update_count": N, "allowed_updates": [...]}`, with the number of unconfirmed updates and the subscription in force (`null` for every kind). |

Messages, updates, keyboards and the other objects have the shapes of the types in [`pkg/botapi`](../pkg/botapi/types.go).

## getUpdates

The stand delivers two kinds of update: `message`, created by `POST /sim/message`, and `callback_query`, created by `POST /sim/callback` ([sim-api.md](sim-api.md)). Update ids start at 1 and grow by one for every update created; they are never reused, not even after `POST /sim/reset`, because a polling bot remembers the last id it confirmed and would drop anything numbered below it.

| Parameter | Behaviour |
|-----------|-----------|
| `offset` | Confirms every pending update whose id is below it: those are forgotten, and the answer starts at `offset`. Missing or `0` confirms nothing. A negative offset counts from the end of the queue: `-1` keeps the newest update and forgets the rest, `-N` keeps the newest N. |
| `limit` | At most this many updates, 1 to 100. Missing, `0`, negative or above 100 means 100. |
| `timeout` | Seconds to hold the request open while nothing is pending. Missing or `0` answers at once. The wait is capped by `--poll-max` (`Options.MaxPollWait`), 30 s by default. A held request is answered as soon as an update is created, when the wait runs out, or when the server is closed; if the client goes away first, nothing is answered. |
| `allowed_updates` | A JSON array of kinds. A list replaces the subscription, an empty list `[]` means every kind, and a missing parameter or the literal `null` keeps whatever the previous poll asked for. A value that is not a JSON array is refused with `Bad Request: can't parse allowed_updates JSON array`. |

The subscription is remembered between polls, as Telegram remembers it for the token, and survives `POST /sim/reset`. It is applied when an update is created, not when it is delivered: an update of a kind the subscription excludes at that moment is never queued (its id is still used up), and an update already queued is delivered even if a later poll narrows the subscription. Updates stay pending until a later poll confirms them with its `offset`; a fault scheduled for `getUpdates` answers before the offset is applied, so a failed poll confirms nothing.

A message the person sends gets a `bot_command` entity when its text starts with a command (`/start`, `/help@tgfake_bot`), and a `mention` entity when `mention` asked for the bot's `@username` in front of it. With `mention` the text starts with the mention, so it carries no `bot_command` entity. Entity offsets and lengths are counted in UTF-16 code units, as Telegram counts them.

## Chats and messages

A chat comes into being the first time either side writes to it. A positive id is a private chat and a negative id a group, unless the person's first message named another type. Message ids are numbered per chat from 1. A private chat the bot writes to before the person does carries no `first_name` or `username` until the person writes.

The bot's messages carry `from` as the bot's user. `parse_mode` is recorded and shown in the transcript, but the text is stored as sent: no formatting is parsed and no entities are produced for the bot's messages.

**Replies.** A send replies to a message named by `reply_parameters` (`{"message_id": N, "allow_sending_without_reply": true}`) or by the older `reply_to_message_id` with `allow_sending_without_reply=true`. The answer carries `reply_to_message`, the quoted message without its own quote. A reply to a message that is not in the chat, or was deleted, is refused unless `allow_sending_without_reply` is set, in which case the message goes out unthreaded.

**Keyboards.** Only inline keyboards are kept. A `reply_markup` of another kind (a reply keyboard, `remove_keyboard`, `force_reply`), an empty `inline_keyboard` array or a value that is not a JSON object is accepted and dropped. `sendPhoto` and `sendDocument` keep an inline keyboard under the file and `sendRichMessage` under the rich message, held to the same rules as a text message's. A button must have exactly one action: `callback_data`, `url` or `web_app`; `web_app` buttons are covered in [mini-apps.md](mini-apps.md).

**Edits.** Only the bot's own messages can be edited. `editMessageText` replaces the text and the keyboard (a call without `reply_markup` leaves the message with none) and records the new `parse_mode`; `editMessageReplyMarkup` replaces the keyboard only. An edit that changes neither the text nor the keyboard is refused. Two keyboards are the same when every button has the same text, `callback_data`, `url` and `web_app` address.

**Deletes.** `deleteMessage` marks a message, the bot's or the person's, as deleted. The text transcript leaves it out, the JSON transcript shows it with `"deleted": true`, and it can no longer be edited, replied to or tapped.

**Photos and documents.** Only uploads are accepted: the file is the multipart part named `photo` or `document`. File ids are `file_1`, `file_2` and so on, with `file_unique_id` `ufile_1` and so on. A photo must decode as PNG, JPEG or GIF and is described by one `PhotoSize` with its real width and height; a document's `mime_type` is detected from its content. The bytes are kept and served at `GET /sim/file/{file_id}`. In the outbox a stored upload is recorded under its parameter as `<file name> (<size> bytes)`.

**Callback queries.** A tap through the simulation API mints a query id `cbq-1`, `cbq-2` and so on. A query takes one answer: once answered it is forgotten, so a second answer is refused like an unknown one, and so is an answer to a query minted before a reset. The answers are listed in the chat's `callbacks` with their `text` and `show_alert`.

**Rich messages and drafts (Bot API 10.1).** `sendRichMessage` stores a message whose text is the `markdown` of `rich_message`, else its `html`, and which the transcript marks as rich. `sendRichMessageDraft` keeps the latest revision of each `draft_id` in the chat (its `markdown` only) and counts the revisions. A draft is not a message: it expires 30 seconds after its last revision (`draftLifetime`), each revision renewing the lifetime, and an expired draft leaves no trace in the transcript. The calls stay in the outbox.

**Typing.** `sendChatAction` makes the chat show "typing…" for 5 seconds (`typingWindow`), whatever the `action`.

## Limits

| Limit | Value |
|-------|-------|
| `text` of `sendMessage` and `editMessageText` | 1 to 4096 characters, counted as Unicode code points. |
| `caption` of `sendPhoto` and `sendDocument` | At most 1024 characters. |
| `callback_data` of a button | At most 64 bytes. |
| `photo` upload | 10 MiB. |
| `document` upload | 50 MiB. |
| `limit` of `getUpdates` | 100. |
| a draft's lifetime | 30 seconds after its last revision. |
| "typing…" after `sendChatAction` | 5 seconds. |

## Strict where Telegram is strict

The stand refuses what api.telegram.org refuses, with the HTTP status as `error_code` and the description in Telegram's wording, so the mistake shows up in the test and not in a user's chat. Every refusal is a 400 unless stated otherwise. One rule is stricter than Telegram on purpose: a button with more than one action, which Telegram reads as its first, is refused so an ambiguous keyboard never passes.

| Call | Refused when | Description |
|------|--------------|-------------|
| any method | the token is not the one of `--token` (401) | `Unauthorized` |
| any method the stand does not serve | always (404) | `Not Found: method not found` |
| a path that is not `/bot<token>/<method>` | always (404) | `Not Found` |
| `getUpdates` | `allowed_updates` is not a JSON array | `Bad Request: can't parse allowed_updates JSON array` |
| `setMyCommands` | `commands` is missing or not a JSON list of commands | `Bad Request: can't parse commands JSON object` |
| `sendMessage`, `sendPhoto`, `sendDocument`, `editMessageText`, `editMessageReplyMarkup`, `deleteMessage`, `sendChatAction`, `sendRichMessage`, `sendRichMessageDraft` | `chat_id` is missing, zero or not an integer | `Bad Request: chat_id is empty` |
| `sendMessage`, `editMessageText` | `text` is empty | `Bad Request: message text is empty` |
| `sendMessage`, `editMessageText` | `text` is over 4096 characters | `Bad Request: message is too long` |
| `sendMessage`, `sendPhoto`, `sendDocument`, `sendRichMessage`, `editMessageText`, `editMessageReplyMarkup` | `inline_keyboard` is not an array, `null` included: what a library sends for a keyboard built from no rows | `Bad Request: Field "inline_keyboard" must be of type Array` |
| same | a button has no action, only `text` | `Bad Request: Text buttons are not allowed in the inline keyboard` |
| same | a button has more than one of `callback_data`, `url` and `web_app` (stricter than Telegram, see above) | `Bad Request: BUTTON_TYPE_INVALID` |
| same | `callback_data` is over 64 bytes | `Bad Request: BUTTON_DATA_INVALID` |
| same | a `web_app` button in a chat that is not private | `Bad Request: BUTTON_TYPE_INVALID` |
| same | a `web_app` button whose address is not absolute http(s) | `Bad Request: inline keyboard button Web App URL '<url>' is invalid` |
| same | a `web_app` button to plain http on a host other than this machine | `Bad Request: inline keyboard button Web App URL '<url>' is invalid: Only HTTPS links are allowed` |
| `sendMessage`, `sendPhoto`, `sendDocument`, `sendRichMessage` | `reply_parameters` is not a JSON object | `Bad Request: can't parse reply parameters JSON object` |
| same | the message replied to is not in the chat or was deleted, and `allow_sending_without_reply` is not set | `Bad Request: message to be replied not found` |
| `sendPhoto`, `sendDocument` | no file part of that name, for example a `file_id` or a URL in place of an upload | `Bad Request: there is no photo in the request` (`document` for `sendDocument`) |
| `sendPhoto`, `sendDocument` | the upload is over 10 MiB (photo) or 50 MiB (document) | `Bad Request: file is too big` |
| `sendPhoto`, `sendDocument` | `caption` is over 1024 characters | `Bad Request: message caption is too long` |
| `sendPhoto` | the upload is not a PNG, JPEG or GIF image | `Bad Request: IMAGE_PROCESS_FAILED` |
| `editMessageText`, `editMessageReplyMarkup` | the message is not in the chat or was deleted | `Bad Request: message to edit not found` |
| same | the message is the person's, not the bot's | `Bad Request: message can't be edited` |
| same | neither the text nor the keyboard would change | `Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message` |
| `deleteMessage` | the message is not in the chat or was already deleted | `Bad Request: message to delete not found` |
| `answerCallbackQuery` | `callback_query_id` is empty | `Bad Request: callback_query_id is empty` |
| same | the stand never issued the query, it was minted before a reset, or it was already answered | `Bad Request: query is too old and response timeout expired or query ID is invalid` |
| `sendRichMessage` | `rich_message` is not a JSON object with a non-empty `markdown` or `html` | `Bad Request: can't parse rich_message JSON object` |
| `sendRichMessageDraft` | `draft_id` is missing, zero or not an integer | `Bad Request: draft_id must be a non-zero integer` |
| same | `rich_message` is missing or not a JSON object | `Bad Request: can't parse rich_message JSON object` |
| `setChatMenuButton` | `menu_button` is not a JSON object | `Bad Request: can't parse menu button JSON object` |
| same | `type` is not `commands`, `web_app` or `default` | `Bad Request: unsupported menu button type` |
| same | a `web_app` button with empty `text` | `Bad Request: menu button text is empty` |
| same | a `web_app` button with no `web_app` object | `Bad Request: menu button Web App URL '' is invalid` |
| same | a `web_app` address Telegram would not open | `Bad Request: menu button Web App URL '<url>' is invalid`, with `: Only HTTPS links are allowed` for plain http to another host |
| `setChatMenuButton`, `getChatMenuButton` | `chat_id` is given and is not a positive integer (a group's id included) | `Bad Request: Invalid chat_id specified` |

`setChatMenuButton` checks the button before the chat, as the Bot API does. A refused call stores no message, replaces no keyboard and uses up no callback query.

## Not implemented

- Methods that are not in the [table above](#methods) answer `404` with `Not Found: method not found` (unless a fault is scheduled for them). That covers, among others, `getFile`, `forwardMessage`, `copyMessage`, `sendVideo`, `sendAudio`, `sendVoice`, `sendSticker`, `sendMediaGroup`, `editMessageCaption`, `editMessageMedia`, `setWebhook`, `getChat`, `getChatMember`, `answerInlineQuery`, `answerWebAppQuery` and every payment, sticker and chat administration method.
- Webhooks: only `deleteWebhook` and `getWebhookInfo` exist, and the stand never calls a bot. Updates are delivered by `getUpdates` alone.
- The person sends text only. There are no incoming photos, documents, voice messages, locations, contacts or other media, no `web_app_data` service messages from a Mini App, and no `edited_message`, `channel_post`, `inline_query`, `chosen_inline_result`, `my_chat_member`, `chat_member` or `message_reaction` updates: the stand creates `message` and `callback_query` updates only.
- Inline mode, payments, games, polls, stickers, forum topics and business connections are not modelled.
- `inline_message_id`: messages are addressed by `chat_id` and `message_id` only.
- Formatting: `parse_mode` is not parsed, so `can't parse entities` never happens on its own. Schedule a fault with `contains` to see it ([sim-api.md](sim-api.md#faults)).
- Reply keyboards (`keyboard`, `remove_keyboard`, `force_reply`) are accepted and dropped, not shown.
- Rate limits are not enforced. A `429` happens only when a fault is scheduled.
- Two `getUpdates` requests at once are both answered from the same queue; there is no `409 Conflict`.
