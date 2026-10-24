package chatpage_test

import (
	"encoding/json"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The edges of the in-app browser, in headless Chrome like its feature.
func TestInAppBrowserEdges(t *testing.T) {
	chrome := requireChrome(t)

	t.Run("an address ends where its sentence and its brackets do", func(t *testing.T) {
		st := newStand(t)
		wiki := st.site + "Foo_(bar)"
		text := "See " + st.site + ". Or (" + st.site + "), or " + st.site + "? Also (" + wiki + "), ((" + st.site + ")) and [" + st.site + "]."
		if err := st.send(url.Values{"text": {text}}); err != nil {
			t.Fatal(err)
		}
		dom := dumpDOM(t, chrome, st.base+"/")
		var got []string
		for _, l := range regexp.MustCompile(`<a\b[^>]*\bhref="([^"]*)"`).FindAllStringSubmatch(dom, -1) {
			got = append(got, l[1])
		}
		want := []string{st.site, st.site, st.site, wiki, st.site, st.site}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("links %q, want %q", got, want)
		}
		if err := expectIn(dom, regexp.QuoteMeta(st.site)+`</a>\. Or \(<a\b`, "the sentence around the links"); err != nil {
			t.Error(err)
		}
	})

	t.Run("an address in capitals is a link too", func(t *testing.T) {
		st := newStand(t)
		upper := strings.Replace(st.site, "http://", "HTTP://", 1)
		if err := st.send(url.Values{"text": {"Go to " + upper}}); err != nil {
			t.Fatal(err)
		}
		dom := dumpDOM(t, chrome, st.base+"/")
		if err := expectIn(dom, `<a\b[^>]*\bhref="`+regexp.QuoteMeta(upper)+`"`, "the link"); err != nil {
			t.Error(err)
		}
	})

	t.Run("a url button that is not a web address opens in a new tab", func(t *testing.T) {
		st := newStand(t)
		if err := st.send(url.Values{"text": {"Mail us"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Mail","url":"mailto:team@example.com"}]]}`}}); err != nil {
			t.Fatal(err)
		}
		dom := dumpDOM(t, chrome, st.base+"/")
		if err := expectIn(dom, `<button\b[^>]*\btitle="Opens mailto:team@example\.com in a new tab"`, "the mailto button"); err != nil {
			t.Error(err)
		}
		got := st.tap(t, chrome, `(await until(() => [...doc.querySelectorAll('#log button')].find((b) => b.textContent === 'Mail'))).click();`)
		if got.Open || len(got.Opened) != 1 || got.Opened[0] != "mailto:team@example.com" {
			t.Errorf("want mailto: in a new tab and a closed browser, got %+v", got)
		}
	})

	t.Run("an unknown size keeps the default phone", func(t *testing.T) {
		st := newStand(t)
		// constructor and __proto__ are names every object has.
		for _, size := range []string{"999x999", "constructor", "__proto__"} {
			dom := dumpDOM(t, chrome, st.base+"/?browser="+url.QueryEscape(st.site)+"&size="+size)
			// 844 less the 47 px status bar and the 69 px address bar.
			if err := expectIn(dom, `id="browserStatus"[^>]*>WebView 390\x{00d7}728 \x{00b7} page 390\x{00d7}728\b`, "the default size for "+size); err != nil {
				t.Error(err)
			}
		}
	})

	t.Run("every screen size gives the site its width and the height under the address bar", func(t *testing.T) {
		st := newStand(t)
		for size, want := range map[string]string{"360x640": "360×551", "390x844": "390×728", "430x932": "430×804", "440x956": "440×825"} {
			dom := dumpDOM(t, chrome, st.base+"/?browser="+url.QueryEscape(st.site)+"&size="+size+"&theme=light")
			if err := expectIn(dom, `id="browserStatus"[^>]*>WebView `+want+` \x{00b7} page `+want+`\b`, size); err != nil {
				t.Error(err)
			}
			if err := expectIn(dom, `<div class="tgb light" id="browserPhone"`, size+" in the light theme"); err != nil {
				t.Error(err)
			}
		}
	})

	t.Run("a desktop scrollbar that takes width from the site is named", func(t *testing.T) {
		st := newStand(t)
		dom := dumpDOM(t, chrome, st.base+"/?browser="+url.QueryEscape(st.site+"tall/")+"&size=440x956")
		status := regexp.MustCompile(`id="browserStatus"[^>]*>([^<]*)<`).FindStringSubmatch(dom)
		if status == nil {
			t.Fatalf("no status line:\n%s", excerpt(dom))
		}
		scrollbar := 0
		if m := regexp.MustCompile(`scrollbar takes (\d+) px`).FindStringSubmatch(status[1]); m != nil {
			scrollbar, _ = strconv.Atoi(m[1])
		}
		want := "page " + strconv.Itoa(440-scrollbar) + "×825"
		if !regexp.MustCompile(regexp.QuoteMeta(want) + `\b`).MatchString(status[1]) {
			t.Errorf("status %q, want %q for a %d px scrollbar", status[1], want, scrollbar)
		}
	})

	t.Run("back and forward step through the addresses opened, close forgets them", func(t *testing.T) {
		st := newStand(t)
		got := st.tap(t, chrome, `
			const b = win.__tgfakeBrowser, back = doc.getElementById('browserBack'), forward = doc.getElementById('browserForward');
			const site = location.origin + '/site/';
			b.open(site); b.open(site + 'tall/');
			out.value = { two: [b.index, b.history.length, back.disabled, forward.disabled] };
			back.click();
			out.value.back = [b.url, b.index, back.disabled, forward.disabled];
			forward.click();
			out.value.forward = [b.url, b.index];
			b.back(); b.open(site + 'other/');
			out.value.branch = b.history.slice();
			doc.getElementById('browserClose').click();
			out.value.closed = [b.url, b.history.length, b.index];`)
		var steps map[string][]any
		if err := json.Unmarshal(got.Value, &steps); err != nil {
			t.Fatalf("value %s: %v", got.Value, err)
		}
		tall, other := st.site+"tall/", st.site+"other/"
		for name, want := range map[string][]any{
			"two":     {1.0, 2.0, false, true},
			"back":    {st.site, 0.0, true, false},
			"forward": {tall, 1.0},
			"branch":  {st.site, other},
			"closed":  {"", 0.0, -1.0},
		} {
			if !reflect.DeepEqual(steps[name], want) {
				t.Errorf("%s: %v, want %v", name, steps[name], want)
			}
		}
		if got.Open {
			t.Error("the browser is still open after Close")
		}
	})

	t.Run("the hook refuses an address that is not a web address", func(t *testing.T) {
		st := newStand(t)
		got := st.tap(t, chrome, `out.value = win.__tgfakeBrowser.open('javascript:alert(1)');`)
		if string(got.Value) != "false" || got.Open || got.URL != "" || !reflect.DeepEqual(got.Events, []string{"refused"}) {
			t.Errorf("want a refusal and a closed browser, got %+v value %s", got, got.Value)
		}
	})
}
