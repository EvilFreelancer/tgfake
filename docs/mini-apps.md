# Mini Apps

This page covers what the tgfake server does around a bot's Mini App: the `web_app` buttons and the menu button a bot sets, the launch a person starts by opening one, with launch parameters and launch data signed the way Telegram signs it, and the phone frame of the chat page that plays the Telegram client for the app. The server side is [`pkg/server/webapp.go`](../pkg/server/webapp.go), the stateless parts are the package [`pkg/webapp`](../pkg/webapp/webapp.go), and the phone frame is in [`internal/chatpage/page.html`](../internal/chatpage/page.html).

## web_app buttons

A bot offers its Mini App with an inline keyboard button that carries `web_app` instead of `callback_data` or `url`:

```json
{"inline_keyboard":[[{"text":"Open the app","web_app":{"url":"https://app.example.com/?session=sess_1"}}]]}
```

The stand accepts it in `sendMessage`, `editMessageText` and `editMessageReplyMarkup` on two conditions, as Telegram does:

- The chat is private. A `web_app` button in a group or supergroup is refused with `Bad Request: BUTTON_TYPE_INVALID`. A chat the stand has not seen yet counts as private when its id is positive.
- The address is one Telegram would open ([`webapp.URLProblem`](../pkg/webapp/webapp.go)): an absolute `https` URL, or plain `http` on this machine, meaning the host `localhost` or a loopback IP such as `127.0.0.1` or `::1`. Telegram itself never opens plain http; the stand allows it on loopback so a Mini App served by a dev server on a local port can be opened from the chat page. Anything else is refused with `Bad Request: inline keyboard button Web App URL '<url>' is invalid`, and plain http to another host adds `: Only HTTPS links are allowed`.

A `web_app` button is not tapped through `POST /sim/callback`: it is opened as a [launch](#launches). On the chat page it is marked with an arrow and opens the app in the [phone frame](#the-phone-frame).

## The menu button

`setChatMenuButton` and `getChatMenuButton` keep the menu button the way the Bot API does:

| Call | Effect |
|------|--------|
| `setChatMenuButton` without `chat_id` | Sets the bot's own button, which every private chat without one of its own shows. |
| `setChatMenuButton` with `chat_id` | Sets a private chat's own button. |
| `getChatMenuButton` without `chat_id` | The bot's button. |
| `getChatMenuButton` with `chat_id` | The chat's own button, else the bot's. |

`menu_button` is a [`MenuButton`](../pkg/botapi/types.go) object:

| `type` | Meaning |
|--------|---------|
| `commands` | The list of the bot's commands. A bot that set nothing shows this. |
| `web_app` | A button with `text` that opens `web_app.url` as a Mini App. The text must not be empty and the address follows the same rule as a `web_app` button. |
| `default` | No value of its own: on a chat it removes the chat's own button, so the chat follows the bot's again; on the bot it puts the bot's button back to `commands`. A call without `menu_button` means `default`. |

```bash
curl -s http://127.0.0.1:18790/bot123:TEST/setChatMenuButton \
  --data-urlencode 'menu_button={"type":"web_app","text":"Open","web_app":{"url":"https://app.example.com/"}}'
curl -s http://127.0.0.1:18790/bot123:TEST/getChatMenuButton -d chat_id=4242
```

```json
{"ok":true,"result":{"type":"web_app","text":"Open","web_app":{"url":"https://app.example.com/"}}}
```

Telegram shows a bot's menu button in private chats only. A `chat_id` that is not a positive integer, a group's id included, is refused with `Bad Request: Invalid chat_id specified`, and the transcript of a group chat always reports `{"type":"commands"}`. The button a chat shows is the `menu_button` of `GET /sim/chat/{id}`, and the bot's own is the `menu_button` of `GET /sim/state`. On the chat page a `web_app` menu button appears as `☰ <text>` left of the message field. `POST /sim/reset` forgets both kinds of button.

## Launches

A launch is a person opening a Mini App. The simulation API starts one with `POST /sim/webapp/launch` and a Go test with `Server.LaunchWebApp` ([go.md](go.md)). Both take a [`WebAppLaunch`](../pkg/server/webapp.go):

| Field | Default | Meaning |
|-------|---------|---------|
| `url` | none | The app's address, as a `web_app` button would open it. Empty opens the menu button of `chat_id` instead. |
| `chat_id` | none | The chat whose menu button is opened when `url` is empty; without it the bot's own menu button is used. |
| `user_id`, `username`, `first_name` | user 4242 `alice`, `Alice` | The person, with the defaults of [`POST /sim/message`](sim-api.md#post-simmessage). |
| `start_param` | none | The start parameter, as a `startapp` link passes it. |
| `color_scheme` | `dark` | `light` gives the light theme, anything else the dark one. |
| `platform` | `tgfake` | The value of `tgWebAppPlatform`. |
| `version` | `10.1` | The value of `tgWebAppVersion`. |

The stand does not check that the bot actually sent a button with the given `url`; it checks the address with the same rule as a button. A launch is refused, with `400` and the reason as `error` through the simulation API, when:

| Reason | When |
|--------|------|
| `no bot token to sign with yet: let the bot call the stand first, or start it with --token` | The stand has no token: it was started without `--token` and no Bot API call has reached it yet. |
| `no Mini App to open: give a url, or let the bot set a web_app menu button` | `url` is empty and the menu button in question is not a `web_app` button. |
| `Web App URL '<url>' is invalid` (with `: Only HTTPS links are allowed` for plain http to another host) | The address is not one Telegram would open. |

### The token

Launch data is signed with the bot's token. A stand started with `--token` (or `Options.Token`) signs with that token. Otherwise it signs with the token in the path of the latest Bot API call, whatever that call was, so the launch checks out against the token the bot is running with. `Server.Token` reports it.

### The launch data

The launch data, `tgWebAppData` (the `initData` of Telegram's Web App script), is a query string of these fields:

| Field | Value |
|-------|-------|
| `query_id` | `tgfake-1`, `tgfake-2` and so on, numbered for the life of the process. |
| `user` | The person as JSON: `id`, `first_name`, `username`, `language_code` (`en`) and `allows_write_to_pm` (`true`). |
| `auth_date` | The time of the launch in Unix seconds. |
| `start_param` | Only when `start_param` was given. |
| `hash` | [`webapp.Sign`](../pkg/webapp/webapp.go) of the other fields under the bot's token. |

`hash` is computed as Telegram documents it for validating data received via the Mini App: the data-check-string is every field but `hash`, sorted by key, written as `key=value` lines joined by `\n`; the secret key is the HMAC-SHA256 of the token under the key `WebAppData`; the hash is the hex HMAC-SHA256 of the data-check-string under that secret key. The Ed25519 `signature` Telegram adds for third parties is left out, because only Telegram holds that key.

### The launch address

The stand puts the launch parameters into the fragment of the app's address ([`webapp.AppendLaunchParams`](../pkg/webapp/webapp.go)), URL-encoded:

| Parameter | Value |
|-----------|-------|
| `tgWebAppData` | The launch data. |
| `tgWebAppVersion` | `version`, `10.1` by default. |
| `tgWebAppPlatform` | `platform`, `tgfake` by default, so an app that tells platforms apart sees the stand as a platform of its own. |
| `tgWebAppThemeParams` | The [theme parameters](#theme-parameters) as JSON. |

An address without a fragment gets the parameters as its whole fragment. An address with a fragment of its own, such as a hash router's `#/route`, keeps it and gets the parameters after a `?`, the form the official SDK parses, or after a `&` when that fragment already carries a query. A start parameter also goes into the query of the address as `tgWebAppStartParam`, where Telegram puts it.

```bash
curl -s -X POST http://127.0.0.1:18790/sim/webapp/launch \
  -d '{"url": "https://app.example.com/#/s/sess_1", "start_param": "abc", "color_scheme": "light"}'
```

The answer is a [`LaunchedWebApp`](../pkg/server/webapp.go), shown here with the long values cut:

```json
{
  "url": "https://app.example.com/?tgWebAppStartParam=abc#/s/sess_1?tgWebAppData=auth_date%3D1791385535%26hash%3D...&tgWebAppPlatform=tgfake&tgWebAppThemeParams=...&tgWebAppVersion=10.1",
  "init_data": "auth_date=1791385535&hash=4998...&query_id=tgfake-1&start_param=abc&user=%7B%22allows_write_to_pm%22%3Atrue%2C...%7D",
  "theme_params": {"bg_color": "#ffffff", "text_color": "#000000", "...": "..."},
  "version": "10.1",
  "platform": "tgfake"
}
```

`url` is what a Telegram client would load in its WebView; `init_data` is the launch data alone, for a test that hands it to the app's server directly.

## Theme parameters

A launch carries the colours of a Telegram theme in `tgWebAppThemeParams`, and `GET /sim/webapp/theme/{light|dark}` serves the same map, which is what the chat page sends an open app in `theme_changed` ([`webapp.ThemeParams`](../pkg/webapp/webapp.go)):

| Key | `light` | `dark` |
|-----|---------|--------|
| `bg_color` | `#ffffff` | `#212121` |
| `secondary_bg_color` | `#efeff3` | `#0f0f0f` |
| `section_bg_color` | `#ffffff` | `#212121` |
| `header_bg_color` | `#ffffff` | `#212121` |
| `bottom_bar_bg_color` | `#ffffff` | `#212121` |
| `text_color` | `#000000` | `#ffffff` |
| `hint_color` | `#999999` | `#aaaaaa` |
| `subtitle_text_color` | `#999999` | `#aaaaaa` |
| `section_header_text_color` | `#6d6d72` | `#8774e1` |
| `link_color` | `#2481cc` | `#8774e1` |
| `accent_text_color` | `#2481cc` | `#8774e1` |
| `button_color` | `#2481cc` | `#8774e1` |
| `button_text_color` | `#ffffff` | `#ffffff` |
| `destructive_text_color` | `#ff3b30` | `#ff595a` |

## Checking launch data in a Mini App's test

A Mini App's server has to check the launch data its page sends it before trusting the user in it. [`webapp.Validate`](../pkg/webapp/webapp.go) does that check: it parses the launch data, recomputes the hash with the token and returns the fields, or `webapp.ErrBadHash` for data without a hash or with one the token does not produce (and a parse error for a string that is not a query string). It does not judge `auth_date`; how old a launch may be is the app's own decision. An app written in Go can call it in production; for an app in another language it is the reference its own check is tested against.

A Go test of an app's launch handler, with the stand in-process:

```go
package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/EvilFreelancer/tgfake/pkg/webapp"
)

const botToken = "123456:TEST"

func TestLaunchDataFromTelegramIsAccepted(t *testing.T) {
	fake := server.New(server.Options{Token: botToken})
	launch, err := fake.LaunchWebApp(server.WebAppLaunch{
		URL:        "https://app.example.com/",
		StartParam: "sess_1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The stand signed what Telegram would sign.
	fields, err := webapp.Validate(launch.InitData, botToken)
	if err != nil || fields.Get("start_param") != "sess_1" {
		t.Fatalf("launch data: %v %v", fields, err)
	}

	// The app's own handler, given the launch data its page posts, accepts it
	// and refuses it once a field has been tampered with.
	handler := NewLaunchHandler(botToken) // the app under test
	post := func(initData string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(initData)))
		return rec.Code
	}
	if code := post(launch.InitData); code != http.StatusOK {
		t.Fatalf("genuine launch data refused: %d", code)
	}
	if code := post(strings.Replace(launch.InitData, "sess_1", "sess_2", 1)); code == http.StatusOK {
		t.Fatal("tampered launch data accepted")
	}
}
```

`NewLaunchHandler` stands for the app's own code. With `Options.Token` set the launch needs no Bot API call first; without it, let the bot (or the test) call any method with its token before launching.

## The phone frame

The chat page at `/` opens a Mini App in a phone frame next to the chat, the way web.telegram.org frames one: the app runs in an iframe, and the page answers its Web App events as a Telegram client would. Everything the page does goes through the simulation API, so the launch it opens is the same one `POST /sim/webapp/launch` returns.

### Opening and closing

A `web_app` button of a message (marked with an arrow, its tooltip naming the address) or the `☰` menu button left of the message field opens the app. The page posts a launch with the chat id, user id and username of the form at the top, the theme and the Bot API version of the Mini App toolbar, and loads the launch address in the frame. The header shows `@<bot username>`, and a "loading…" cover stays over the app until it sends `web_app_ready`. The header's Close button closes the app, after a confirmation dialog when the app has asked for one with `web_app_setup_closing_behavior`.

### Controls

| Control | Effect |
|---------|--------|
| size | The phone's screen: `390×844` (the default), `360×640` or `430×932`. |
| window | `expanded` (the default) or `half open`. Half open, the WebView keeps its height and the frame shows its top 55 %, the chat still in sight above it, as on a phone. |
| theme | `dark` (the default) or `light`: the theme of the next launch, and a `theme_changed` event to the open app. |
| fullscreen insets | Hides the 48 px header and reports the safe areas of a phone in fullscreen: a safe area of 47 px at the top and 34 px at the bottom, and a content safe area of 46 px at the top. |
| keyboard | Shows a 260 px on-screen keyboard under the WebView, which shrinks it by that much, as an Android keyboard does. |
| Bot API | The `version` of the next launch, `10.1` by default. |
| Reload | Loads the same launch address again, with the same launch data. |
| Back | In the header, shown while the app has made its Back button visible with `web_app_setup_back_button`; a click sends `back_button_pressed`. |
| Close | In the header; see above. |

Changing the size, the window, the insets, the keyboard or the theme sends the app `viewport_changed`, `safe_area_changed` and `content_safe_area_changed`, and `theme_changed` for a theme change. A status line under the phone shows the WebView's size, the visible height, the window state, whether vertical swipes are on, whether closing asks for confirmation and whether the app is ready.

The viewport the app is told about follows the phone: the WebView is the screen height less the 48 px header (none with fullscreen insets) and less the keyboard when it shows, and the visible height is that, or 55 % of it half open.

### Events

The app talks to the page as to any Telegram web client: it posts `{"eventType": ..., "eventData": ...}`, as a JSON string or an object, to its parent window, and the page posts JSON strings of the same shape back to the frame, addressed to the app's origin. The page acts on these events from the app:

| Event from the app | What the page does |
|--------------------|--------------------|
| `web_app_ready` | Marks the app ready and removes the loading cover. |
| `web_app_expand` | Expands a half-open window and sends `viewport_changed`. |
| `web_app_request_viewport` | Sends `viewport_changed` with `height`, `is_expanded` and `is_state_stable`. |
| `web_app_request_theme` | Sends `theme_changed` with `theme_params`. |
| `web_app_request_safe_area` | Sends `safe_area_changed` with `top`, `bottom`, `left` and `right`. |
| `web_app_request_content_safe_area` | Sends `content_safe_area_changed`. |
| `web_app_setup_back_button` | Shows or hides the Back button by `is_visible`. |
| `web_app_setup_swipe_behavior` | Records `allow_vertical_swipe`; shown in the status line only. |
| `web_app_setup_closing_behavior` | Records `need_confirmation`, which Close then honours. |
| `web_app_set_header_color` | Paints the header with `color` and picks a black or white title that reads on it; a `color_key` is recorded only. |
| `web_app_set_background_color` | Paints the area behind the WebView with `color`. |
| `web_app_set_bottom_bar_color` | Records `color`. |
| `web_app_open_link` | Opens `url` in a new browser tab. |
| `web_app_close` | Closes the app without asking. |

Any other event is logged and otherwise ignored: the page draws no main or secondary button, shows no popups and delivers no `web_app_data` to the bot. The event log under the phone lists every event in both directions, `← app` for what the app sent and `→ app` for what the page sent, with its data.

### Hooks for browser tests

The page keeps its Mini App state in `window.__tgfakeMiniApp`, so a browser test (Playwright, Puppeteer, Selenium) can drive the frame and read what happened without clicking through the toolbar:

| Member | What it is |
|--------|------------|
| `events` | Every event both ways, oldest first, as `{dir, type, data, at}`: `dir` is `in` (from the app) or `out` (to the app, plus the page's own `open` and `closed` entries), `at` is a millisecond timestamp. |
| `url`, `origin` | The launch address loaded and its origin. |
| `ready`, `expanded`, `backVisible`, `swipes`, `closingConfirmation` | The state the app has set or the controls chose. |
| `headerColor`, `backgroundColor`, `bottomBarColor` | The colours the app asked for. |
| `open(url)` | Launches and loads an app; an empty `url` opens the chat's menu button. Returns a promise. |
| `close()`, `back()` | Close the app; press its Back button. |
| `setState({size, window, theme, fullscreen, keyboard})` | Changes the controls (`size` as `"390x844"`, `window` as `"expanded"` or `"half"`, `theme` as `"dark"` or `"light"`, the other two booleans) and sends the app the events a change sends. |
| `post(type, data)` | Sends the app any event. |
| `receive(type, data)` | Handles an event as if the app had sent it. |
| `sendViewport()`, `sendTheme()` | Send `viewport_changed`, `theme_changed`. |
| `webViewHeight()`, `visibleHeight()`, `safeArea()`, `contentSafeArea()` | The values the page reports to the app. |

A Playwright test that opens an app half open in the light theme and checks what the app asked for:

```javascript
await page.goto('http://127.0.0.1:18790/');
await page.evaluate(() => window.__tgfakeMiniApp.open('http://127.0.0.1:5173/'));
await page.waitForFunction(() => window.__tgfakeMiniApp.ready);
await page.evaluate(() => window.__tgfakeMiniApp.setState({ window: 'half', theme: 'light' }));
const fromApp = await page.evaluate(() =>
  window.__tgfakeMiniApp.events.filter((e) => e.dir === 'in').map((e) => e.type));
```

The launch is signed with the bot's token, so let the bot reach the stand first, or start the stand with `--token`.
