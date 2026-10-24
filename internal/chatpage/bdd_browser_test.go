package chatpage_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/cucumber/godog"
)

// The in-app browser lives in the page's script, so its scenarios run the
// chat page in headless Chrome and read the document Chrome ends up with.
func TestInAppBrowserFeature(t *testing.T) {
	chrome := requireChrome(t)
	suite := godog.TestSuite{
		Name: "Telegram stand in-app browser",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var base, site, dom string
			sc.Given(`^the chat page of a stand and a site to open$`, func() {
				base, site = standWithSite(t)
			})
			sc.When(`^the bot sends the person the site's address with a url button to it$`, func() error {
				res, err := http.PostForm(base+"/bot123:TEST/sendMessage", url.Values{
					"chat_id":      {"4242"},
					"text":         {"Our site: " + site + " - have a look"},
					"reply_markup": {`{"inline_keyboard":[[{"text":"Open the site","url":"` + site + `"}]]}`},
				})
				if err != nil {
					return err
				}
				_ = res.Body.Close()
				if res.StatusCode != http.StatusOK {
					return fmt.Errorf("sendMessage answered %d", res.StatusCode)
				}
				dom = dumpDOM(t, chrome, base+"/")
				return nil
			})
			sc.Then(`^the chat shows the address as a link to the site$`, func() error {
				return expectIn(dom, `<a\b[^>]*\bhref="`+regexp.QuoteMeta(site)+`"[^>]*>`+regexp.QuoteMeta(site)+`</a>`, "a link to the site")
			})
			sc.Then(`^the url button opens the site in the in-app browser$`, func() error {
				return expectIn(dom, `<button\b[^>]*\btitle="Opens `+regexp.QuoteMeta(site)+` in the in-app browser"[^>]*>Open the site</button>`, "the url button")
			})
			sc.When(`^the person opens (\S+) in the in-app browser$`, func(address string) {
				dom = dumpDOM(t, chrome, base+"/?browser="+url.QueryEscape(address))
			})
			sc.When(`^the person opens the site in the in-app browser of a (\d+x\d+) phone$`, func(size string) {
				dom = dumpDOM(t, chrome, base+"/?browser="+url.QueryEscape(site)+"&size="+size)
			})
			sc.Then(`^the site is laid out (\d+) CSS pixels wide and (\d+) high$`, func(w, h string) error {
				return expectIn(dom, `id="browserStatus"[^>]*>[^<]*\bpage `+w+`\x{00d7}`+h+`\b`, "the size the site sees")
			})
			sc.Then(`^the address bar shows the host of the site$`, func() error {
				u, _ := url.Parse(site)
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

// standWithSite serves the stand and, on the same origin, a small site under
// /site/, so the chat page can read the size the site is laid out at.
// /site/tall/ is a page taller than any phone, which scrolls.
func standWithSite(t *testing.T) (base, site string) {
	t.Helper()
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
	mux.Handle("/", fake.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { fake.Close(); srv.Close() })
	return srv.URL, srv.URL + "/site/"
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
