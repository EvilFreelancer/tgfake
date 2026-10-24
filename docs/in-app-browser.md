# In-app browser

Telegram opens a link a person taps in a chat, a web address in a message or a `url` button, in a browser of its own, unless the person has chosen to open links in a browser app. The site there is laid out at the width of the phone's screen, under Telegram's address bar, with Telegram's toolbar floating over the bottom of the page. The chat page at `/` plays that browser in a phone frame next to the chat, so a site can be looked at, and tested, at the size a Telegram user sees it, with no phone. The browser lives in the page's script, [`internal/chatpage/page.html`](../internal/chatpage/page.html); the server keeps no state for it, because Telegram does not tell a bot that a person opened a link.

## Opening a link

| What the person taps | What the chat page does |
|----------------------|-------------------------|
| A web address in the text or caption of a message | Telegram finds the addresses in any message and shows them as links; the page does the same for `http://` and `https://` addresses, in any letter case. Punctuation that ends a sentence is not part of an address, nor is a closing parenthesis or square bracket the address did not open, so `(https://en.wikipedia.org/wiki/Foo_(bar))` links to `.../Foo_(bar)`. |
| A `url` button | The button is marked with an arrow, and its tooltip says where it opens. |
| A `web_app` button | Opens the Mini App in its own phone frame ([mini-apps.md](mini-apps.md#the-phone-frame)), not in the browser. |

**open links in**, at the top of the chat, chooses where a tap goes: `in-app browser`, the default, as in Telegram, or `new tab`, as for a person who opens links in a browser app. Only `http` and `https` addresses open in the in-app browser; a `url` button with any other scheme, such as `mailto:` or `tg://`, opens in a new tab either way.

The address of the chat page itself can open the browser, which is the quickest way to look at a site in it:

```text
http://127.0.0.1:18790/?browser=http%3A%2F%2F127.0.0.1%3A5173%2F&size=440x956&theme=light
```

`browser` is the address to open, `size` one of the screen sizes below and `theme` `dark` or `light`; a missing or unknown size or theme keeps the default. An address that is not `http` or `https`, such as `javascript:alert(1)`, is not opened, and a line under the chat says why.

## The phone

The site loads in a frame as wide as the phone's screen, so its media queries and its layout see that width. The geometry is that of Telegram for iOS, measured on an iPhone with a 440×956 screen: the status bar of the screen size, the address bar 69 px under it, and the site from there down to the bottom of the screen, with the toolbar 48 px high floating 26 px above the bottom, over the site.

| Screen | Status bar | WebView, the size the site is laid out at |
|--------|------------|-------------------------------------------|
| `390×844` (the default) | 47 px | 390×728 |
| `360×640` | 20 px | 360×551 |
| `430×932` | 59 px | 430×804 |
| `440×956` | 62 px | 440×825 |

The address bar holds Close on the left, the host of the address in the middle, with the whole address as its tooltip, and the menu on the right. The toolbar holds Back, Forward, Share, Bookmark and Open in a new tab.

| Control | Effect |
|---------|--------|
| size | The phone's screen, from the table above. |
| theme | `dark` (the default) or `light`: the colours of Telegram's bars. The site follows the colour scheme of the browser that shows the chat page, as it follows the phone's in Telegram. |
| Reload | Loads the address again; the menu has the same item. |
| Close | Closes the browser and forgets its history. |
| Back, Forward | Step through the addresses the browser was asked to open. A site of another origin keeps its own navigation to itself, so a link followed inside the site is not a step. |
| Share, Copy link | Copy the address to the clipboard, standing for Telegram's share sheet and the Copy Link item of its menu. |
| Bookmark | Adds the address to the browser's bookmarks. |
| Open in a new tab | Opens the address in a tab of the browser that shows the chat page, standing for Telegram's button that hands the page to the browser app. |

A status line under the phone shows the size of the WebView, the size the site is laid out at when the site has the chat page's origin (the client width and height of its document; a site of another origin keeps it to itself), the heights of the status bar and the address bar, and a warning when the browser showing the chat page takes the width of its scrollbar from a page that scrolls. The log under it lists what happened: `open`, `load`, `reload`, `back`, `forward`, `share`, `copy`, `bookmark`, `open_external`, `refused` and `closed`.

## What the frame does not reproduce

- **The engine.** The site runs in the browser that shows the chat page, with its engine, user agent and fonts. Telegram for iOS shows sites in WebKit with Safari's user agent; to look at a site the way an iPhone renders it, show the chat page in Safari or in Playwright's WebKit.
- **Desktop scrollbars.** A phone draws its scrollbar over the page. A desktop browser with classic scrollbars, as on Windows and Linux, takes their width, 15 px or so, from a page that scrolls, so the page is laid out that much narrower than on the phone, while its media queries still see the full width. The status line names the width when the browser does that. Safari and Chrome on macOS usually draw overlay scrollbars, and Playwright's headless browsers draw none.
- **Telegram's font size.** The menu of Telegram's browser scales the text of a site from 50 % to 150 % by putting `-webkit-text-size-adjust` into the page. A frame cannot style a site of another origin, so the chat page has no such control.
- **Sites that refuse a frame.** A site that sends `X-Frame-Options` or a `frame-ancestors` rule that leaves the chat page out is refused inside the frame by the browser itself. Open it in a new tab, or point the browser at a development server of the site.
- **The bars on scroll.** Telegram shrinks its address bar as the page scrolls; the frame keeps it at its full height.
- **A site that leaves the frame.** The frame is not sandboxed, as Telegram's browser is a whole browser, so a site that navigates the top window, with a `target="_top"` link or from a script, replaces the chat page itself.

## Hooks for browser tests

The page keeps the browser's state in `window.__tgfakeBrowser`, and the site loads in the frame `#browserFrame`, so a browser test (Playwright, Puppeteer, Selenium) can open a site and look inside it without clicking through the chat:

| Member | What it is |
|--------|------------|
| `url` | The address loaded, empty while the browser is closed. |
| `history`, `index` | The addresses the browser was asked to open, oldest first, and the position of the one loaded. |
| `events` | Every entry of the log, oldest first, as `{type, data, at}`; `at` is a millisecond timestamp. |
| `bookmarks` | The addresses bookmarked. |
| `page` | `{width, height}` the site is laid out at when it has the chat page's origin, else `null`. |
| `open(url)` | Opens an address in the browser; returns `false` and opens nothing for an address that is not `http` or `https`. |
| `close()`, `reload()`, `back()`, `forward()` | What the buttons of the same names do. |
| `share()`, `copy()`, `bookmark()`, `openExternally()` | What Share, Copy link, Bookmark and Open in a new tab do. |
| `setState({size, theme})` | Changes the controls (`size` as `"440x956"`, `theme` as `"dark"` or `"light"`). |
| `viewport()` | `{width, height}` of the WebView for the chosen screen. |

A Playwright test that opens a site on the largest phone and measures it from inside the frame, which Playwright can do for a site of any origin:

```javascript
await page.goto('http://127.0.0.1:18790/?browser=' + encodeURIComponent('http://127.0.0.1:5173/') + '&size=440x956');
const site = page.frameLocator('#browserFrame');
await site.locator('h1').waitFor();
const width = await site.locator('html').evaluate(() => innerWidth); // 440
```
