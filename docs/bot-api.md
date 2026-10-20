# The Bot API surface

This page describes the part of the tgfake server a bot talks to: the Bot API methods it serves, how it reads their parameters, what it answers, the refusals it shares with Telegram and what it does not implement. The code is in [`pkg/server`](../pkg/server): [`methods.go`](../pkg/server/methods.go), [`updates.go`](../pkg/server/updates.go), [`chat.go`](../pkg/server/chat.go), [`faults.go`](../pkg/server/faults.go), [`webapp.go`](../pkg/server/webapp.go) and [`response.go`](../pkg/server/response.go). The person's side of the same chats is the [simulation API](sim-api.md).

## Addressing

A bot calls `<origin>/bot<token>/<method>` where it would call `https://api.telegram.org/bot<token>/<method>`. Any HTTP method works. The method name is matched without regard to case (`getMe`, `getme` and `GETME` are the same method) and a trailing slash after it is ignored.

Any token is accepted unless the command was started with `--token` (or a Go test set `Options.Token`); then every other token is answered `401 Unauthorized`. A download from `/file/bot<token>/<file_path>` with another token is answered with the same `404 Not Found` as an unknown file, the way api.telegram.org's file endpoint does. Without a fixed token the stand remembers the token of the latest call, which is the token Mini App launch data is signed with ([mini-apps.md](mini-apps.md)).

The chat page at `/` and the routes under `/sim/` are served by the stand itself. Every other path that is not of the form `/bot<token>/<method>`, a request to a `/sim/` path with the wrong HTTP method included, is answered with the Bot API's own 404:

```json
{"ok":false,"error_code":404,"description":"Not Found"}
```

## Parameters

Parameters are read the way the Bot API reads them, so any library works unchanged ([`parseParams`](../pkg/server/methods.go)):

| Request | What is read |
|---------|--------------|
| `Content-Type: multipart/form-data` | The query string, form fields and file parts, including nested `attach://` references. |
| `Content-Type: application/json` | The query string and the top-level keys of a JSON object (up to 4 MB). A string value is taken as is, `null` is an empty value, and any other value (a number, a boolean, an object, an array) is kept as its JSON text, so `"reply_markup": {...}` and `"reply_markup": "{...}"` mean the same. |
| anything else | The query string and an `application/x-www-form-urlencoded` body. |

A parameter whose value is a Bot API object (`reply_markup`, `reply_parameters`, `allowed_updates`, `commands`, `scope`, `menu_button`, `rich_message`, `prices`) is JSON text, as on Telegram. A Boolean parameter (`ok`, `show_alert`, `protect_content`, `can_stop`, ...) is true when it reads `true`, `yes` or `1` in any case, as the Bot API server reads it, and false otherwise. A parameter the stand does not read is accepted, recorded in the outbox with the call, and has no other effect.

Send and edit methods require a non-zero integer `chat_id`. Chat information methods also resolve a configured `@channelusername`. Inline edits take `inline_message_id` instead of `chat_id` and `message_id`.

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
| `getMe` | nothing | The bot's `User`: `id` 7000000001, `is_bot`, `first_name` (`--bot-name`), `username` (`--bot-username`), `can_join_groups` true, `can_read_all_group_messages` false, `supports_inline_queries` from `--inline` (false by default). |
| `getUpdates` | `offset`, `limit`, `timeout`, `allowed_updates` | An array of `Update`; see [getUpdates](#getupdates). |
| `setMyCommands` | `commands`, `scope`, `language_code` | `true`. One list per scope and per language; see [Commands](#commands). |
| `getMyCommands` | `scope`, `language_code` | The list registered for exactly that scope and language, `[]` when there is none. |
| `deleteMyCommands` | `scope`, `language_code` | `true`. Forgets that one list; a wider one shows through again. |
| `sendMessage` | `chat_id`, `text`, `parse_mode`, `reply_markup`, `reply_parameters` or `reply_to_message_id` and `allow_sending_without_reply` | The sent `Message`. |
| `getFile` | `file_id` | A `File` with `file_path`, downloadable from `/file/bot<token>/<file_path>`. |
| `sendPhoto` | `chat_id`, `photo` (upload, known file id or URL), `caption`, `parse_mode`, `reply_markup`, reply parameters, `protect_content` | The sent `Message` with one `PhotoSize`. |
| `sendDocument` | Same, with `document` | The sent `Message` with a `Document`. |
| `sendVideo` | Same, with `video`; `width`, `height`, `duration` | The sent `Message` with a `Video`. |
| `sendAnimation` | Same, with `animation`; `width`, `height`, `duration` | The sent `Message` with an animation. |
| `sendAudio` | Same, with `audio`; `duration`, `performer`, `title` | The sent `Message` with an `Audio`. |
| `sendVoice` | Same, with `voice`; `duration` | The sent `Message` with a voice message. |
| `sendVideoNote` | `chat_id`, `video_note` (upload or known id), `duration`, `length`, keyboard, reply and protection parameters | The sent `Message` with a video note; no caption. |
| `sendMediaGroup` | `chat_id`, `media` (array of photo/video/audio/document input media), reply and protection parameters | An array of messages sharing a `media_group_id`. Photo/video may mix; audio and document albums must be homogeneous. |
| `sendLocation` | `chat_id`, `latitude`, `longitude`, keyboard, reply and protection parameters | A `Message` with a static location. |
| `sendVenue` | Same, plus `title`, `address`, Foursquare/Google place fields | A `Message` with a venue and its location. |
| `sendContact` | `chat_id`, `phone_number`, `first_name`, `last_name`, `vcard`, keyboard, reply and protection parameters | A `Message` with a contact. |
| `editMessageText` | `chat_id`, `message_id`, `text`, `parse_mode`, `reply_markup` | The edited `Message`, with `edit_date`. |
| `editMessageReplyMarkup` | `chat_id`, `message_id`, `reply_markup` | The edited `Message`. Without `reply_markup` the keyboard is removed. |
| `editMessageCaption` | Message target, `caption`, `parse_mode`, `reply_markup` | The edited `Message`, or `true` for an inline target. |
| `editMessageMedia` | Message target, `media` (photo/video/animation/audio/document), `reply_markup` | The edited `Message`, or `true` for an inline target. Album media keeps its type restrictions. |
| `copyMessage` | `chat_id`, `from_chat_id`, `message_id`, optional `caption`, keyboard, reply and protection parameters | A `MessageId`; the copy has no forward origin. |
| `forwardMessage` | `chat_id`, `from_chat_id`, `message_id`, `protect_content` | A `Message` with `forward_origin`; protected source messages cannot be forwarded. |
| `copyMessages`, `forwardMessages` | Destination/source chat ids, `message_ids` array; `remove_caption` for copies, `protect_content` | An array of `MessageId`; missing/deleted source messages are skipped and album grouping is preserved. |
| `deleteMessage` | `chat_id`, `message_id` | `true`. |
| `deleteMessages` | `chat_id`, `message_ids` array | `true`; missing messages in a known chat are skipped. |
| `sendChatAction` | `chat_id` (`action` is recorded, not read) | `true`. The chat shows "typing…" for 5 seconds. |
| `answerCallbackQuery` | `callback_query_id`, `text`, `show_alert` | `true`. The answer is kept in the chat's `callbacks`. |
| `sendInvoice` | `chat_id`, `title`, `description`, `payload`, `currency`, `prices`, `start_parameter`, `reply_markup`, the reply and protection parameters | The sent `Message` with an `Invoice`; its `total_amount` is the sum of the prices. |
| `answerPreCheckoutQuery` | `pre_checkout_query_id`, `ok`, `error_message` | `true`. Confirming one delivers the `successful_payment` service message. |
| `sendRichMessage` | `chat_id`, `rich_message` (`{"markdown": ...}` or `{"html": ...}`), `reply_markup`, the reply parameters | The sent `Message`; its `text` is the markdown, else the HTML. |
| `sendRichMessageDraft` | `chat_id`, `draft_id`, `rich_message`, `can_stop`, `keep_on_stop`, `message_thread_id` | `true`. |
| `setChatMenuButton` | `chat_id` (optional), `menu_button` | `true`; see [mini-apps.md](mini-apps.md#the-menu-button). |
| `getChatMenuButton` | `chat_id` (optional) | The `MenuButton` the chat shows. |
| `answerInlineQuery` | `inline_query_id`, `results` | `true`; keeps offered results for selection through `/sim/inline/choose`. |
| `answerWebAppQuery` | `web_app_query_id`, `result` | A `SentWebAppMessage`; sends a message on behalf of the user of an issued Mini App launch. |
| `savePreparedInlineMessage` | `user_id`, `result`, `allow_user_chats`, `allow_bot_chats`, `allow_group_chats`, `allow_channel_chats` | A `PreparedInlineMessage` for sharing through `/sim/webapp/share`. |
| `setWebhook` | `url`, `secret_token`, `allowed_updates`, `drop_pending_updates`, `max_connections`, `ip_address`, uploaded `certificate` | `true`; delivers queued updates by HTTP POST. An empty URL deletes the webhook. |
| `deleteWebhook` | `drop_pending_updates` | `true`; stops delivery and optionally clears the queue. |
| `getWebhookInfo` | nothing | URL, custom certificate flag, pending count, subscription, connection limit, configured IP and last delivery error. |
| `getChat` | `chat_id` | A `ChatFullInfo` for a known chat. |
| `getChatMember` | `chat_id`, `user_id` | A configured/inferred `ChatMember`; a known user outside the chat has status `left`. |
| `getChatAdministrators` | `chat_id` | Every creator/administrator member, other bots included, ordered by user id. |
| `getChatMemberCount` | `chat_id` | The number of present members. |
| `getUserProfilePhotos` | `user_id`, `offset`, `limit` | Configured profile photos; a known user has an empty list by default. |

Messages, updates, keyboards and the other objects have the shapes of the types in [`pkg/botapi`](../pkg/botapi/types.go).

## getUpdates

The stand creates `message` (text, incoming media, `web_app_data` or a payment service message), `callback_query`, `pre_checkout_query`, `inline_query`, `chosen_inline_result` and `stopped_message_generation` updates through the [simulation API](sim-api.md). Update ids start at 1 and grow by one for every update created; they are never reused, not even after `POST /sim/reset`, because a polling bot remembers the last id it confirmed and would drop anything numbered below it. Polling while a webhook is active returns HTTP 409; delete the webhook first.

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

**Keyboards.** Only inline keyboards are kept. A `reply_markup` of another kind (a reply keyboard, `remove_keyboard`, `force_reply`), an empty `inline_keyboard` array or a value that is not a JSON object is accepted and dropped. Text, media, contact/location and rich messages keep keyboards under the same rules. A button must have exactly one action: `callback_data`, `url`, `web_app`, `switch_inline_query`, `switch_inline_query_current_chat`, `copy_text` or `pay`. Switch buttons open inline search on the chat page; copy buttons use the browser clipboard. `web_app` buttons are covered in [mini-apps.md](mini-apps.md). A `pay` button is the "Pay" button of an invoice: an invoice keyboard must lead with one, when it is sent and when it is edited, and any other message refuses one with `Bad Request: BUTTON_TYPE_INVALID`.

**Edits.** The bot's own messages and messages sent via this bot can be edited. `editMessageText` replaces text and keyboard (omitted `reply_markup` removes it) and records `parse_mode`; `editMessageReplyMarkup` replaces the keyboard only. Caption/media edits use the same target and keyboard rules. An unchanged edit is refused. Keyboard equality includes every supported button action. Inline targets return `true`; ordinary targets return the edited message.

**Deletes.** `deleteMessage` marks a message, the bot's or the person's, as deleted. The text transcript leaves it out, the JSON transcript shows it with `"deleted": true`, and it can no longer be edited, replied to or tapped.

**Files and media.** A file parameter accepts a multipart upload, an `attach://part_name` reference, a known file id of the same type or an HTTP(S) URL (except video notes). File ids are `file_1`, `file_2` and so on, with `file_unique_id` `ufile_1` and so on. A photo must decode as PNG, JPEG or GIF and is described by one `PhotoSize` with its real dimensions. Other media bytes are opaque: MIME type is detected, while dimensions, duration, performer and title come from supplied metadata; no codec probing or transcoding occurs. Incoming media uses the same file store; an incoming photo larger than 90 pixels on its long side also gets a 90-pixel JPEG thumbnail, listed first among its sizes, and its caption carries `caption_entities` for a mention and a leading command. `getFile` supplies the authenticated download path, while `/sim/file/{file_id}` serves the bytes to tests and the chat page. A stored upload is recorded as `<file name> (<size> bytes)` in the outbox.

**Inline and Mini App messages.** Enable `--inline` (`Options.InlineMode`) before injecting an inline query. Results support article, photo, video, audio, voice, document, gif, mpeg4_gif, static location, venue and contact; input message content supports text, static location, venue and contact. Media is resolved when selected, so cached ids must belong to this stand and remote URLs must be reachable. Selection adds a user message with `via_bot` and queues `chosen_inline_result`. A result with a keyboard also gets an `inline_message_id`, usable by all four edit methods. `answerWebAppQuery` consumes an issued launch query once. Prepared messages have a deterministic five-minute lifetime in the stand and enforce the designated user and allowed destination types.

**Webhooks.** Updates are delivered oldest first, one request at a time. A 2xx response confirms the update; failures retain it, update `last_error_date`/`last_error_message` and retry after one second. A JSON response containing `method` and parameters can call a Bot API method. Changing or deleting the webhook cancels an in-flight request without losing its queued update; `Close` cancels delivery and media URL downloads. Localhost/loopback HTTP URLs and arbitrary local ports are allowed so a test can run its receiver offline. Other URLs must be HTTPS on Telegram's allowed ports. Uploaded PEM certificates extend the client's trust roots. `max_connections` is reported but delivery stays serialized; `ip_address` is reported without overriding DNS.

**Chat information.** A chat starts with the bot as a member; injected users become members unless a test configured their status. `/sim/chat/configure` supplies metadata and membership, including administrators, restricted and absent users. `/sim/user/profile` supplies photo sets using known photo ids. Configured information is copied and reset with the session; querying it never changes membership.

**Callback queries.** A tap through the simulation API mints a query id `cbq-1`, `cbq-2` and so on. A query takes one answer: once answered it is forgotten, so a second answer is refused like an unknown one, and so is an answer to a query minted before a reset. The answers are listed in the chat's `callbacks` with their `text` and `show_alert`.

**Rich messages and drafts (Bot API 10.1).** `sendRichMessage` stores a message whose text is the `markdown` of `rich_message`, else its `html`, and which the transcript marks as rich. `sendRichMessageDraft` keeps the latest revision of each `draft_id` in the chat (its `markdown` only) and counts the revisions. A draft is not a message: it expires 30 seconds after its last revision (`draftLifetime`), each revision renewing the lifetime, and an expired draft leaves no trace in the transcript. The calls stay in the outbox.

**Stopping a draft (Bot API 10.3).** A revision with `can_stop=true` shows the person a Stop button under the draft; each revision replaces the previous one's `can_stop` and `keep_on_stop`. Pressing it (`POST /sim/draft/stop`) queues a `stopped_message_generation` update carrying the chat, the revision's `message_thread_id` when it had one, and the `draft_id`. The draft leaves the transcript at once unless its latest revision set `keep_on_stop=true`, in which case it stays without the Stop button until it expires; a new revision with `can_stop=true` shows the button again. A later revision of a stopped draft is accepted: the Bot API documentation does not say what Telegram answers to one.

**Typing.** `sendChatAction` makes the chat show "typing…" for 5 seconds (`typingWindow`), whatever the `action`.

## Commands

`setMyCommands` keeps one list per `scope` and per `language_code`, as Telegram keeps them, each command trimmed and without a leading `/`; no `commands` at all, or `null`, is an empty list. `getMyCommands` answers the list registered for exactly the scope and the language it names, `[]` when there is none ("If commands aren't set, an empty list is returned"), and `deleteMyCommands` forgets exactly that list so a wider one shows through again.

What a user sees in the menu is another list: the one the chain the Bot API documents under "Determining list of commands" reaches first, walked from the user's scope downwards. The stand resolves it in [`Server.CommandsFor`](go.md#reading-what-the-bot-did). From `chat` in a private chat the chain is `chat` with the language, `chat`, `all_private_chats` with the language, `all_private_chats`, `default` with the language, `default`. From `chat_member` in a group it is `chat_member`, `chat_administrators` (administrators only), `chat`, `all_chat_administrators` (administrators only), `all_group_chats`, `default`, each tried with the language before without one. A list registered for another language answers nothing and leaves the next step of the chain to answer.

A scope names its chat by integer id, given as a number or a string of digits, or by the `@username` of a configured group or channel, the way the chat information methods resolve one; a username the stand cannot resolve is answered the way Telegram answers it, `Bad Request: chat not found`. A chat with a positive id the stand has not seen is a private chat, a negative one a group, so the administrator and member scopes are refused for the first. `is_ephemeral` of a command is kept and answered back, but a message is never ephemeral.

## Payments

`sendInvoice` stores an invoice message. Its `total_amount` is the sum of the `prices`, and the `payload` is kept for the pre-checkout query and the payment service message: it is not part of the wire `Invoice` object, as on Telegram.

The person pays an invoice with `POST /sim/invoice/pay`, which queues a `pre_checkout_query`. The query carries the currency, the total and the payload of the invoice itself, so a bot that re-checks the price against its own catalogue meets whatever the invoice said - including a price that has moved on since. The query takes exactly one answer: `answerPreCheckoutQuery` refuses the second one the way it refuses a query it never issued. Confirming one with `ok=true` appends a `successful_payment` service message to the chat and queues it, as Telegram does once the money is taken; refusing one takes no money and delivers nothing. `POST /sim/payment` delivers a payment service message of its own, for a charge taken elsewhere and for a `refunded_payment`. On the chat page an invoice shows its Pay button, Telegram's own when its keyboard has none, and a tap on it is `POST /sim/invoice/pay`.

`start_parameter` is kept on the invoice and answered back on the wire. `provider_token`, `max_tip_amount`, `suggested_tip_amounts`, `provider_data`, the photo and `need_*` fields are recorded and have no other effect. Order info and shipping are not modelled: neither `PreCheckoutQuery` nor `SuccessfulPayment` carries an `order_info`.

## Limits

| Limit | Value |
|-------|-------|
| `text` of `sendMessage` and `editMessageText` | 1 to 4096 characters, counted as Unicode code points. |
| `caption` of `sendPhoto` and `sendDocument` | At most 1024 characters. |
| `callback_data` of a button | At most 64 bytes. |
| `photo` upload | 10 MiB. |
| other media upload | 50 MiB. |
| URL download | 5 MiB for photos, 20 MiB for other media. |
| `getFile` download | 20 MiB. |
| a photo the person sends | width and height adding up to at most 10000 pixels. |
| media album | At most 10 items; the Bot API server also accepts a singleton. |
| `message_ids` array | 1 to 100 ids. |
| inline results | At most 50; result ids contain 1 to 64 bytes. |
| prepared message | 5 minutes in the stand. |
| `copy_text.text` | 1 to 256 characters. |
| a bot command | 1 to 32 characters after trimming and dropping a leading `/`; its description 1 to 256 characters after trimming. |
| `title` of `sendInvoice` | 1 to 32 characters. |
| `description` of `sendInvoice` | 1 to 255 characters. |
| `payload` of `sendInvoice` | 1 to 128 bytes. |
| `prices` of `sendInvoice` | At least one item, exactly one for payments in Telegram Stars; an item may be negative (a discount), the total has to be positive. |
| `limit` of `getUpdates` | 100. |
| a draft's lifetime | 30 seconds after its last revision. |
| "typing…" after `sendChatAction` | 5 seconds. |

## Strict where Telegram is strict

The stand refuses what api.telegram.org refuses, with the HTTP status as `error_code` and the description in Telegram's wording, so the mistake shows up in the test and not in a user's chat. Every refusal is a 400 unless stated otherwise. One rule is stricter than Telegram on purpose: a button with more than one action, which Telegram reads as its first, is refused so an ambiguous keyboard never passes.

| Call | Refused when | Description |
|------|--------------|-------------|
| any method | the token is not the one of `--token` (401) | `Unauthorized` |
| a `/file/bot<token>/` download | the token is not the one of `--token` (404) | `Not Found` |
| any method the stand does not serve | always (404) | `Not Found: method not found` |
| a path that is not `/bot<token>/<method>` | always (404) | `Not Found` |
| `getUpdates` | `allowed_updates` is not a JSON array | `Bad Request: can't parse allowed_updates JSON array` |
| `setMyCommands` | `commands` is not JSON / not an array | `Bad Request: Can't parse commands JSON object` / `Bad Request: Expected an Array of BotCommand` |
| same | a command is not an object, or has no `command` or `description` | `Bad Request: Can't parse BotCommand: expected an Object` / `...: Can't find field "command"` / `...: Can't find field "description"` |
| same | after trimming and dropping a leading `/`, a command is empty or past 32 characters | `Bad Request: Command must be non-empty` / `Bad Request: Command length must not exceed 32` |
| same | after trimming, a description is empty or past 256 characters | `Bad Request: Command description must be non-empty` / `Bad Request: Command description length must not exceed 256` |
| `setMyCommands`, `getMyCommands`, `deleteMyCommands` | `scope` is not JSON / not a JSON object | `Bad Request: Can't parse BotCommandScope JSON object` / `Bad Request: Can't parse BotCommandScope: BotCommandScope must be an Object` |
| same | `scope.type` is missing / not a string / not one of the seven the Bot API defines | `Bad Request: Can't parse BotCommandScope: Can't find field "type"` / `...: Field "type" must be of type String` / `...: Unsupported type specified` |
| same | a chat scope has no `chat_id` / an empty one | `Bad Request: Can't parse BotCommandScope: Can't find field "chat_id"` / `...: Empty chat_id specified` |
| same | a `chat_member` scope has no `user_id` / one that is not an integer / one that is not positive | `Bad Request: Can't parse BotCommandScope: Can't find field "user_id"` / `...: Field "user_id" must be a valid Number` / `...: Invalid user_id specified` |
| same | a chat scope names a username that is not a configured group or channel, or chat 0 | `Bad Request: chat not found` |
| same | a `chat_administrators` or `chat_member` scope names a private chat / a scope names a channel | `Bad Request: Can't use specified scope in private chats` / `Bad Request: Can't change commands in channel chats` |
| same | `language_code` is neither empty nor two lower-case Latin letters | `Bad Request: Invalid language code specified` |
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
| media send | no upload or file reference | `Bad Request: there is no photo in the request` (the corresponding media name for other methods) |
| `sendPhoto`, `sendDocument` | the upload is over 10 MiB (photo) or 50 MiB (document) | `Bad Request: file is too big` |
| `sendPhoto`, `sendDocument` | `caption` is over 1024 characters | `Bad Request: message caption is too long` |
| `sendPhoto` | the upload is not a PNG, JPEG or GIF image | `Bad Request: IMAGE_PROCESS_FAILED` |
| `editMessageText`, `editMessageReplyMarkup` | the message is not in the chat or was deleted | `Bad Request: message to edit not found` |
| same | the message is the person's, not the bot's | `Bad Request: message can't be edited` |
| same | neither the text nor the keyboard would change | `Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message` |
| `deleteMessage` | the message is not in the chat or was already deleted | `Bad Request: message to delete not found` |
| `answerCallbackQuery` | `callback_query_id` is empty | `Bad Request: callback_query_id is empty` |
| same | the stand never issued the query, it was minted before a reset, or it was already answered | `Bad Request: query is too old and response timeout expired or query ID is invalid` |
| `sendInvoice` | `chat_id` is missing, zero or not an integer | `Bad Request: chat_id is empty` |
| same | `title`, `description`, `payload`, `currency` or `prices` is empty, checked in that order | `Bad Request: Parameter "<name>" is required` |
| same | `payload` is not UTF-8 | `Bad Request: The payload must be encoded in UTF-8` |
| same | `prices` is not JSON / not an array | `Bad Request: Can't parse prices JSON object` / `Bad Request: Expected an Array of LabeledPrice` |
| same | a price is not an object, has no or an empty `label`, or no `amount` | `Bad Request: Can't parse LabeledPrice: LabeledPrice must be an Object` / `...: Can't find field "label"` / `...: LabeledPrice label must be non-empty` / `...: Can't find field "amount"` |
| same | an `amount` is not an integer (a number or a string of digits) | `Bad Request: Can't parse LabeledPrice: Field "amount" must be a valid Number` |
| same | `prices` is an empty array | `Bad Request: There must be at least one price part` |
| same | an `amount` is past 999999999999 either way | `Bad Request: Too big amount of the currency specified` |
| same | the total of the prices is zero or negative / past 999999999999 | `Bad Request: Total price must be positive` / `Bad Request: Total price is too big` |
| same | `title`, `description` or `payload` is past the length above | `Bad Request: title is too long` / `description is too long` / `payload is too long` |
| same | `currency` is `XTR` and `prices` does not hold exactly one item | `Bad Request: prices must contain exactly one item` |
| same | a keyboard does not lead with a Pay button | `Bad Request: BUTTON_TYPE_INVALID` |
| `answerPreCheckoutQuery` | `ok` is not true and `error_message` is empty | `Bad Request: Parameter "error_message" is required` |
| same | `pre_checkout_query_id` is empty, the stand never issued the query, it was minted before a reset, or it was already answered | `Bad Request: query is too old and response timeout expired or query ID is invalid` |
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

Additional refusals for the extended methods:

| Call | Refused when | Description |
|------|--------------|-------------|
| `getFile` | no id / unknown id / file over 20 MiB | `Bad Request: file_id not specified` / `Bad Request: wrong file_id or the file is temporarily unavailable` / `Bad Request: file is too big` |
| media send | unknown or wrong-type file id / oversized caption or bytes | The same file-id, caption and size errors as above. |
| `sendMediaGroup` | empty / over 10 items / mixed audio or document album | `Bad Request: there are no messages to send` / `Bad Request: too many messages to send as an album` / `Bad Request: audio can't be mixed with other media types` (or `document`) |
| media edit | target missing, deleted, not editable or unchanged | The same edit errors as above. |
| `copyMessage`, `forwardMessage` | missing/deleted source | `Bad Request: message to copy not found` / `Bad Request: message to forward not found` |
| `forwardMessage` | protected source | `Bad Request: message can't be forwarded` |
| bulk copy/forward/delete | missing, malformed, empty, oversized or invalid ids | `Bad Request: message identifiers are not specified`, `can't parse message_ids JSON object`, `expected an Array of message identifiers`, `too many message identifiers specified` or `invalid message identifier specified`, each with `Bad Request: `. |
| location send | missing coordinates / invalid coordinates | `Bad Request: latitude is empty` / `Bad Request: longitude is empty` / `Bad Request: invalid location specified` |
| contact send | missing phone or first name | `Bad Request: phone_number is empty` / `Bad Request: first_name is empty` |
| inline answer | unknown/already answered query / over 50 results / invalid or duplicate result id | The query error above / `Bad Request: too many inline query results specified` / `Bad Request: RESULT_ID_INVALID` / `Bad Request: RESULT_ID_DUPLICATE`. |
| `answerWebAppQuery` | launch query unknown, reset or already consumed | The query error above. |
| chat information | chat unknown / invalid or unknown user | `Bad Request: chat not found` / `Bad Request: invalid user_id specified` / `Bad Request: user not found` |
| `getUpdates` | webhook active (409) | `Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first` |
| `setWebhook` | malformed URL / external HTTP / unsupported external port | `Bad Request: invalid webhook URL specified` / `Bad Request: bad webhook: An HTTPS URL must be provided for webhook` / `Bad Request: bad webhook: Webhook can be set up only on ports 80, 88, 443 or 8443` |
| `setWebhook` | secret over 256 bytes / characters outside A-Z, a-z, 0-9, `_`, `-` | `Bad Request: secret token is too long` / `Bad Request: secret token contains illegal characters` |

The stand checks copied text length with `BUTTON_COPY_TEXT_INVALID`. Certificate decoding failures currently use `Bad Request: invalid certificate` rather than OpenSSL's platform-specific diagnostic, and the length and Telegram Stars refusals of `sendInvoice` above use the wording of the Bot API's own constraints rather than a captured answer; these are deliberate validation approximations. Delivery/network diagnostics come from Go's HTTP client. Advanced parameters not listed here are recorded without affecting state.

## Not implemented

- Methods outside the [table above](#methods) answer `404` with `Not Found: method not found`, including stickers, polls, live photos, games, forum topics and chat administration mutations.
- Incoming location/contact messages, `edited_message`, `channel_post`, membership and reaction updates are not injected. Chat configuration changes no update queue.
- Inline games/stickers, rich input content, cache behavior, pagination buttons and query timeouts are not modelled. `cache_time`, `is_personal`, `next_offset` and the inline results button are only recorded.
- Payments go no further than a bot can see: a payment provider, shipping, order info and `order_info` on the pre-checkout query and the payment are not modelled, `shipping_query` is never sent, and a charge can only be taken or returned - never left pending. `is_ephemeral` of a command is kept but a message is never ephemeral.
- Media thumbnails, codec validation, live locations, full Telegram membership/permission enforcement and service-message copy restrictions are not modelled. Inline media edits currently resolve uploads as ordinary edits; Telegram permits only existing ids and URLs for inline targets.
- Formatting: `parse_mode` is not parsed, so `can't parse entities` never happens on its own. Schedule a fault with `contains` to see it ([sim-api.md](sim-api.md#faults)).
- Reply keyboards (`keyboard`, `remove_keyboard`, `force_reply`) are accepted and dropped, not shown.
- Rate limits are not enforced. A `429` happens only when a fault is scheduled.
- Two concurrent polls are answered from the same queue; only the polling/webhook conflict is implemented.
