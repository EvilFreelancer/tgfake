package chatpage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/cucumber/godog"
)

// The in-app browser lives in the page's script, so its scenarios run the
// chat page in headless Chrome and read the document Chrome ends up with. A
// tap goes through a harness page of the same origin that frames the chat
// page, clicks in it and writes down what happened.
func TestInAppBrowserFeature(t *testing.T) {
	chrome := requireChrome(t)
	suite := godog.TestSuite{
		Name: "Telegram stand in-app browser",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var st *stand
			var dom, setup string
			sc.Given(`^the chat page of a stand and a site to open$`, func() {
				st, dom, setup = newStand(t), "", ""
			})
			sc.When(`^the bot sends the person the site's address with a url button to it$`, func() error {
				return st.send(url.Values{
					"text":         {"Our site: " + st.site + " - have a look"},
					"reply_markup": {`{"inline_keyboard":[[{"text":"Open the site","url":"` + st.site + `"}]]}`},
				})
			})
			sc.When(`^the person chooses to open links in a new tab$`, func() {
				setup = `doc.getElementById('linksIn').value = 'tab';`
			})
			sc.Then(`^the chat shows the address as a link to the site$`, func() error {
				dom = dumpDOM(t, chrome, st.base+"/")
				return expectIn(dom, `<a\b[^>]*\bhref="`+regexp.QuoteMeta(st.site)+`"[^>]*>`+regexp.QuoteMeta(st.site)+`</a>`, "a link to the site")
			})
			sc.Then(`^tapping the url button opens the site in the in-app browser$`, func() error {
				return inAppBrowserOpened(st.tap(t, chrome, setup+tapURLButton), st.site)
			})
			sc.Then(`^tapping the link in the message opens the site in the in-app browser$`, func() error {
				return inAppBrowserOpened(st.tap(t, chrome, setup+tapLink), st.site)
			})
			sc.Then(`^tapping the url button opens the site in a new tab and not in the in-app browser$`, func() error {
				got := st.tap(t, chrome, setup+tapURLButton)
				if got.Open || got.URL != "" || len(got.Opened) != 1 || got.Opened[0] != st.site {
					return fmt.Errorf("want a new tab with %s and a closed browser, got %+v", st.site, got)
				}
				return nil
			})
			sc.When(`^the person opens (\S+) in the in-app browser$`, func(address string) {
				dom = dumpDOM(t, chrome, st.base+"/?browser="+url.QueryEscape(address))
			})
			sc.When(`^the person opens the site in the in-app browser of a (\d+x\d+) phone$`, func(size string) {
				dom = dumpDOM(t, chrome, st.base+"/?browser="+url.QueryEscape(st.site)+"&size="+size)
			})
			sc.Then(`^the site is laid out (\d+) CSS pixels wide and (\d+) high$`, func(w, h string) error {
				return expectIn(dom, `id="browserStatus"[^>]*>[^<]*\bpage `+w+`\x{00d7}`+h+`\b`, "the size the site sees")
			})
			sc.Then(`^the address bar shows the host of the site$`, func() error {
				u, _ := url.Parse(st.site)
				return expectIn(dom, `id="browserHost"[^>]*>`+regexp.QuoteMeta(u.Host)+`</span>`, "the host in the address bar")
			})
			sc.Then(`^the in-app browser stays closed and the chat says why$`, func() error {
				if err := expectIn(dom, `<section id="browser"[^>]*\bhidden`, "a closed browser"); err != nil {
					return err
				}
				return expectIn(dom, `id="linkNotice"[^>]*>[^<]*javascript:alert\(1\)[^<]*http`, "the reason in the chat")
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/in_app_browser.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Telegram stand in-app browser feature failed")
	}
}

// Scripts for stand.tap: the url button and the link of the bot's message.
const (
	tapURLButton = `(await until(() => [...doc.querySelectorAll('#log button')].find((b) => b.textContent === 'Open the site'))).click();`
	tapLink      = `(await until(() => doc.querySelector('#log a.link'))).click();`
)

func inAppBrowserOpened(got tapped, site string) error {
	if !got.Open || got.URL != site || len(got.Opened) != 0 {
		return fmt.Errorf("want %s in the in-app browser and no new tab, got %+v", site, got)
	}
	return nil
}

// stand is the stand and, on the same origin, a small site under /site/, so
// the chat page can read the size the site is laid out at, and the harness
// pages of the taps under /harness/. /site/tall/ is a page taller than any
// phone, which scrolls.
type stand struct {
	base, site string

	mu      sync.Mutex
	scripts map[string]string
}

func newStand(t *testing.T) *stand {
	t.Helper()
	st := &stand{scripts: map[string]string{}}
	fake := server.New(server.Options{MaxPollWait: 50 * time.Millisecond})
	mux := http.NewServeMux()
	mux.HandleFunc("/site/", func(w http.ResponseWriter, r *http.Request) {
		body := `<p>a site</p>`
		if r.URL.Path == "/site/tall/" {
			body = `<div style="height:3000px">a tall site</div>`
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><title>site</title>` + body))
	})
	mux.HandleFunc("/harness/", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		script, ok := st.scripts[r.URL.Query().Get("case")]
		st.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Replace(harnessPage, "/*SCRIPT*/", script, 1)))
	})
	mux.Handle("/", fake.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { fake.Close(); srv.Close() })
	st.base, st.site = srv.URL, srv.URL+"/site/"
	return st
}

// send is the bot's sendMessage to the person.
func (st *stand) send(form url.Values) error {
	form.Set("chat_id", "4242")
	res, err := http.PostForm(st.base+"/bot123:TEST/sendMessage", form)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("sendMessage answered %d", res.StatusCode)
	}
	return nil
}

// tapped is what a harness page writes down after its script ran.
type tapped struct {
	URL     string          `json:"url"`
	History []string        `json:"history"`
	Index   int             `json:"index"`
	Open    bool            `json:"open"`
	Opened  []string        `json:"opened"`
	Events  []string        `json:"events"`
	Value   json.RawMessage `json:"value"`
	Error   string          `json:"error"`
}

// harnessPage frames the chat page and runs a script against it with win
// (the chat page's window), doc (its document), until, sleep and out (an
// object whose value field the result carries). The chat page's window.open
// is replaced so a new tab is written down instead of opened.
const harnessPage = `<!doctype html><title>harness</title><pre id="out">pending</pre>
<iframe id="chat" src="/" style="width:1600px;height:1000px"></iframe>
<script>
const frame = document.getElementById('chat');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(pred) {
  for (let i = 0; i < 200; i++) { const v = pred(); if (v) return v; await sleep(25); }
  throw new Error('timed out waiting for ' + pred);
}
frame.addEventListener('load', async () => {
  const win = frame.contentWindow, doc = frame.contentDocument, opened = [], out = {};
  win.open = (u) => { opened.push(String(u)); return null; };
  try { /*SCRIPT*/ ; await sleep(50); } catch (e) { out.error = String(e); }
  const b = win.__tgfakeBrowser;
  Object.assign(out, { url: b.url, history: b.history, index: b.index, open: !doc.getElementById('browser').hidden,
    opened, events: b.events.map((e) => e.type) });
  document.getElementById('out').textContent = JSON.stringify(out);
}, { once: true });
</script>`

// tap runs a script in a harness page and returns what it wrote down.
func (st *stand) tap(t *testing.T, chrome, script string) tapped {
	t.Helper()
	st.mu.Lock()
	name := strconv.Itoa(len(st.scripts))
	st.scripts[name] = script
	st.mu.Unlock()
	dom := dumpDOM(t, chrome, st.base+"/harness/?case="+name)
	m := regexp.MustCompile(`(?s)<pre id="out">(.*?)</pre>`).FindStringSubmatch(dom)
	if m == nil {
		t.Fatalf("the harness wrote nothing:\n%s", excerpt(dom))
	}
	var got tapped
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &got); err != nil {
		t.Fatalf("the harness wrote %q: %v", m[1], err)
	}
	if got.Error != "" {
		t.Fatalf("the harness script failed: %s", got.Error)
	}
	return got
}

// dumpDOM loads a page in headless Chrome and returns the document once its
// scripts have run.
func dumpDOM(t *testing.T, chrome, page string) string {
	t.Helper()
	profile, err := os.MkdirTemp("", "tgfake-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	// Chrome can hold the profile for a moment after it exits on Windows;
	// a profile left behind in the temp directory is not a failure.
	t.Cleanup(func() { _ = os.RemoveAll(profile) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run",
		"--no-default-browser-check", "--user-data-dir="+profile, "--window-size=1600,1100",
		"--virtual-time-budget=5000", "--dump-dom", page)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			stderr := strings.TrimSpace(string(exit.Stderr))
			if len(stderr) > 2000 {
				stderr = stderr[len(stderr)-2000:]
			}
			t.Fatalf("chrome --dump-dom %s: %v\n%s", page, err, stderr)
		}
		t.Fatalf("chrome --dump-dom %s: %v", page, err)
	}
	return string(out)
}

func expectIn(dom, pattern, what string) error {
	if regexp.MustCompile(pattern).MatchString(dom) {
		return nil
	}
	return fmt.Errorf("%s is not in the page (%s); the page:\n%s", what, pattern, excerpt(dom))
}

// excerpt is the part of the page a failure is about, short enough to read.
func excerpt(dom string) string {
	if i := strings.Index(dom, `<section id="browser"`); i >= 0 {
		dom = dom[i:]
	}
	if len(dom) > 3000 {
		dom = dom[:3000] + "..."
	}
	return dom
}

// requireChrome finds headless Chrome or skips the test, the way the
// serverless tests skip without Docker. TGFAKE_CHROME names a binary that is
// not on the PATH, and TGFAKE_TEST_CHROME=1 fails the test instead of
// skipping it, which CI sets on Linux.
func requireChrome(t *testing.T) string {
	t.Helper()
	chrome := chromePath()
	if chrome == "" {
		if os.Getenv("TGFAKE_TEST_CHROME") == "1" {
			t.Fatal("TGFAKE_TEST_CHROME=1, but no Chrome or Chromium was found; set TGFAKE_CHROME to its binary")
		}
		t.Skip("no Chrome or Chromium found; set TGFAKE_CHROME to run the in-app browser tests")
	}
	return chrome
}

func chromePath() string {
	if p := os.Getenv("TGFAKE_CHROME"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	var known []string
	switch runtime.GOOS {
	case "darwin":
		known = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium"}
	case "windows":
		known = []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`}
	}
	for _, p := range known {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
