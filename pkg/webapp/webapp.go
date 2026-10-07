// Package webapp is what Telegram does around a Mini App launch, with no
// state of its own: the launch data signed with the bot's token and checked
// the way an app checks it (core.telegram.org/bots/webapps, "Validating data
// received via the Mini App"), the launch parameters put into the app's
// address, the colours of a Telegram theme and the rule for which addresses a
// web_app button may open. The tgfake server builds its launches with it; a
// Mini App's own test can check the launch data it was given with Validate.
package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

const (
	// Version is the Bot API version the stand claims as the client's
	// (tgWebAppVersion): the latest the Mini App documentation names.
	Version = "10.1"
	// Platform is what tgWebAppPlatform says: an app that tells platforms
	// apart sees the stand as one of its own.
	Platform = "tgfake"
	// DefaultColorScheme is the theme a launch carries when none is asked.
	DefaultColorScheme = "dark"
)

// IsLoopbackHost reports whether host names this machine: localhost or a
// loopback address. The stand lets a Mini App run there over plain http,
// which Telegram itself never does, so a web app started on a port of this
// machine can be opened from the chat page.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// URLProblem says why Telegram would not open raw as a Mini App, or "" when
// it would: an absolute https address, or plain http on this machine.
func URLProblem(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Sprintf("Web App URL '%s' is invalid", raw)
	}
	if u.Scheme == "http" && !IsLoopbackHost(u.Hostname()) {
		return fmt.Sprintf("Web App URL '%s' is invalid: Only HTTPS links are allowed", raw)
	}
	return ""
}

// AppendLaunchParams puts the encoded launch parameters into the fragment of
// app: the whole fragment when it has none, behind "?" after a fragment of
// its own, behind "&" after one that already carries a query.
func AppendLaunchParams(app, params string) string {
	base, frag, ok := strings.Cut(app, "#")
	switch {
	case !ok || frag == "":
		return base + "#" + params
	case strings.Contains(frag, "?"):
		return app + "&" + params
	default:
		return app + "?" + params
	}
}

// Sign is the hash Telegram puts into launch data: the hex HMAC-SHA256 of
// the data-check-string (every field but hash, sorted by key, as key=value
// lines) under the HMAC-SHA256 of the token keyed "WebAppData".
func Sign(token string, data url.Values) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+data.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}

// ErrBadHash is returned by Validate for launch data that carries no hash, or
// one the token does not produce.
var ErrBadHash = errors.New("webapp: launch data hash does not match the bot's token")

// Validate checks launch data (tgWebAppData, the initData of the Telegram Web
// App script) against the bot's token, the way a Mini App's server should, and
// returns its fields. It does not judge auth_date: how old a launch may be is
// the app's own decision.
func Validate(initData, token string) (url.Values, error) {
	data, err := url.ParseQuery(initData)
	if err != nil {
		return nil, fmt.Errorf("webapp: launch data is not a query string: %w", err)
	}
	got, err := hex.DecodeString(data.Get("hash"))
	if err != nil || len(got) == 0 {
		return nil, ErrBadHash
	}
	want, _ := hex.DecodeString(Sign(token, data))
	if !hmac.Equal(got, want) {
		return nil, ErrBadHash
	}
	return data, nil
}

// ThemeParams are the colours a Telegram client hands a Mini App
// (tgWebAppThemeParams and theme_changed) for its light or dark theme.
func ThemeParams(scheme string) map[string]string {
	if scheme == "light" {
		return map[string]string{
			"bg_color": "#ffffff", "secondary_bg_color": "#efeff3", "section_bg_color": "#ffffff",
			"header_bg_color": "#ffffff", "bottom_bar_bg_color": "#ffffff",
			"text_color": "#000000", "hint_color": "#999999", "subtitle_text_color": "#999999",
			"section_header_text_color": "#6d6d72", "link_color": "#2481cc", "accent_text_color": "#2481cc",
			"button_color": "#2481cc", "button_text_color": "#ffffff", "destructive_text_color": "#ff3b30",
		}
	}
	return map[string]string{
		"bg_color": "#212121", "secondary_bg_color": "#0f0f0f", "section_bg_color": "#212121",
		"header_bg_color": "#212121", "bottom_bar_bg_color": "#212121",
		"text_color": "#ffffff", "hint_color": "#aaaaaa", "subtitle_text_color": "#aaaaaa",
		"section_header_text_color": "#8774e1", "link_color": "#8774e1", "accent_text_color": "#8774e1",
		"button_color": "#8774e1", "button_text_color": "#ffffff", "destructive_text_color": "#ff595a",
	}
}
