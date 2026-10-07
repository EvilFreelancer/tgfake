package webapp

import (
	"errors"
	"net/url"
	"testing"
)

// A fixed vector, computed outside this package (Python's hmac over the
// documented data-check-string), so signing and checking cannot agree on the
// same mistake.
func TestSignKnownVector(t *testing.T) {
	data := url.Values{}
	data.Set("auth_date", "1700000000")
	data.Set("query_id", "AAHdF6IQAAAAAN0XohDhrOrc")
	data.Set("user", `{"id":279058397,"first_name":"Vladislav","username":"vdkfrost","language_code":"ru"}`)
	const want = "d16b987afd609aa3c33232cac13427d98b80535d6e4b3e4025a02a336fd343ef"
	if got := Sign("123456:ABC-DEF1234ghIkl", data); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestValidateAcceptsWhatSignSigned(t *testing.T) {
	data := url.Values{"auth_date": {"1700000000"}, "query_id": {"Q1"}, "user": {`{"id":4242}`}}
	data.Set("hash", Sign("123:TOKEN", data))
	got, err := Validate(data.Encode(), "123:TOKEN")
	if err != nil || got.Get("query_id") != "Q1" || got.Get("user") != `{"id":4242}` {
		t.Fatalf("Validate = %v, %v", got, err)
	}
}

func TestValidateRefusesAnotherTokenATamperedFieldAndNoHash(t *testing.T) {
	data := url.Values{"auth_date": {"1700000000"}, "query_id": {"Q1"}}
	data.Set("hash", Sign("123:TOKEN", data))
	tampered := url.Values{"auth_date": {"1700000001"}, "query_id": {"Q1"}, "hash": {data.Get("hash")}}
	for name, c := range map[string]struct{ initData, token string }{
		"another token":  {data.Encode(), "456:OTHER"},
		"tampered field": {tampered.Encode(), "123:TOKEN"},
		"no hash":        {"auth_date=1700000000&query_id=Q1", "123:TOKEN"},
		"hash not hex":   {"auth_date=1700000000&hash=zz", "123:TOKEN"},
	} {
		if _, err := Validate(c.initData, c.token); !errors.Is(err, ErrBadHash) {
			t.Errorf("%s: Validate = %v, want ErrBadHash", name, err)
		}
	}
	if _, err := Validate("%zz", "123:TOKEN"); err == nil || errors.Is(err, ErrBadHash) {
		t.Errorf("a malformed query: %v", err)
	}
}

func TestAppendLaunchParams(t *testing.T) {
	for app, want := range map[string]string{
		"https://app.example.com/":             "https://app.example.com/#a=1",
		"https://app.example.com/#":            "https://app.example.com/#a=1",
		"https://app.example.com/#/s/1":        "https://app.example.com/#/s/1?a=1",
		"https://app.example.com/#/s/1?view=x": "https://app.example.com/#/s/1?view=x&a=1",
	} {
		if got := AppendLaunchParams(app, "a=1"); got != want {
			t.Errorf("AppendLaunchParams(%q) = %q, want %q", app, got, want)
		}
	}
}

func TestURLProblemAdmitsHTTPSAndThisMachineOnly(t *testing.T) {
	for _, ok := range []string{"https://app.example.com/", "http://127.0.0.1:18792/", "http://localhost:5173/", "http://[::1]:8080/"} {
		if p := URLProblem(ok); p != "" {
			t.Errorf("URLProblem(%q) = %q", ok, p)
		}
	}
	for _, bad := range []string{"http://app.example.com/", "ftp://app.example.com/", "app.example.com", ""} {
		if URLProblem(bad) == "" {
			t.Errorf("URLProblem(%q) admitted it", bad)
		}
	}
}

func TestThemeParamsDarkUnlessLight(t *testing.T) {
	if ThemeParams("light")["bg_color"] != "#ffffff" || ThemeParams("dark")["bg_color"] != "#212121" || ThemeParams("")["bg_color"] != "#212121" {
		t.Fatal("theme colours")
	}
}
