// Package chatpage is the chat page of the tgfake server: the person's side of
// the conversation in a browser, with the bot's keyboards as buttons, a phone
// frame for Mini Apps and the Bot API calls alongside. It is plain HTML and
// script with no outside asset, so it works on a machine with no network, and
// everything it shows it reads from the simulation API.
package chatpage

import (
	_ "embed"
	"net/http"
)

//go:embed page.html
var pageHTML []byte

// Serve writes the page.
func Serve(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(pageHTML)
}
