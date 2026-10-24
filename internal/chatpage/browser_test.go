package chatpage_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"testing"
)

// The edges of the in-app browser, in headless Chrome like its feature.
func TestInAppBrowserEdges(t *testing.T) {
	chrome := requireChrome(t)
	send := func(t *testing.T, base string, form url.Values) {
		t.Helper()
		form.Set("chat_id", "4242")
		res, err := http.PostForm(base+"/bot123:TEST/sendMessage", form)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("sendMessage answered %d", res.StatusCode)
		}
	}

	t.Run("punctuation that ends a sentence is not part of the address", func(t *testing.T) {
		base, site := standWithSite(t)
		send(t, base, url.Values{"text": {"See " + site + ". Or (" + site + "), or " + site + "?"}})
		dom := dumpDOM(t, chrome, base+"/")
		links := regexp.MustCompile(`<a\b[^>]*\bhref="([^"]*)"`).FindAllStringSubmatch(dom, -1)
		if len(links) != 3 {
			t.Fatalf("%d links, want 3: %v", len(links), links)
		}
		for _, l := range links {
			if l[1] != site {
				t.Errorf("link to %q, want %q", l[1], site)
			}
		}
		if err := expectIn(dom, regexp.QuoteMeta(site)+`</a>\. Or \(<a\b`, "the sentence around the links"); err != nil {
			t.Error(err)
		}
	})

	t.Run("a url button that is not a web address opens in a new tab", func(t *testing.T) {
		base, _ := standWithSite(t)
		send(t, base, url.Values{"text": {"Mail us"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Mail","url":"mailto:team@example.com"}]]}`}})
		dom := dumpDOM(t, chrome, base+"/")
		if err := expectIn(dom, `<button\b[^>]*\btitle="Opens mailto:team@example\.com in a new tab"`, "the mailto button"); err != nil {
			t.Error(err)
		}
	})

	t.Run("an unknown size keeps the default phone", func(t *testing.T) {
		base, site := standWithSite(t)
		dom := dumpDOM(t, chrome, base+"/?browser="+url.QueryEscape(site)+"&size=999x999")
		// 844 less the 47 px status bar and the 69 px address bar.
		if err := expectIn(dom, `id="browserStatus"[^>]*>WebView 390\x{00d7}728 \x{00b7} page 390\x{00d7}728\b`, "the default size"); err != nil {
			t.Error(err)
		}
	})

	t.Run("every screen size gives the site its width and the height under the address bar", func(t *testing.T) {
		base, site := standWithSite(t)
		for size, want := range map[string]string{"360x640": "360×551", "390x844": "390×728", "430x932": "430×804", "440x956": "440×825"} {
			dom := dumpDOM(t, chrome, base+"/?browser="+url.QueryEscape(site)+"&size="+size+"&theme=light")
			if err := expectIn(dom, `id="browserStatus"[^>]*>WebView `+want+` \x{00b7} page `+want+`\b`, size); err != nil {
				t.Error(err)
			}
			if err := expectIn(dom, `<div class="tgb light" id="browserPhone"`, size+" in the light theme"); err != nil {
				t.Error(err)
			}
		}
	})

	t.Run("a desktop scrollbar that takes width from the site is named", func(t *testing.T) {
		base, site := standWithSite(t)
		dom := dumpDOM(t, chrome, base+"/?browser="+url.QueryEscape(site+"tall/")+"&size=440x956")
		status := regexp.MustCompile(`id="browserStatus"[^>]*>([^<]*)<`).FindStringSubmatch(dom)
		if status == nil {
			t.Fatalf("no status line:\n%s", excerpt(dom))
		}
		scrollbar := 0
		if m := regexp.MustCompile(`scrollbar takes (\d+) px`).FindStringSubmatch(status[1]); m != nil {
			scrollbar, _ = strconv.Atoi(m[1])
		}
		want := "page " + strconv.Itoa(440-scrollbar) + "\u00d7825"
		if !regexp.MustCompile(regexp.QuoteMeta(want) + `\b`).MatchString(status[1]) {
			t.Errorf("status %q, want %q for a %d px scrollbar", status[1], want, scrollbar)
		}
	})
}
