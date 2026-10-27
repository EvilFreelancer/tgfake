package server

import (
	"net/http"
	"net/url"
	"testing"
)

func TestReplyKeyboardHTTP(t *testing.T) {
	s := newStand(t, Options{})
	send := func(markup string) {
		t.Helper()
		status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}})
		if status != 200 {
			t.Fatalf("send: %d %v", status, body)
		}
	}
	send(`{"keyboard":[[{"text":"Photo"},{"text":"Video"},{"text":"Wallet"}]],"one_time_keyboard":true,"resize_keyboard":true,"is_persistent":true,"input_field_placeholder":"Choose"}`)
	send(`{"inline_keyboard":[[{"text":"Model","callback_data":"model"}]]}`)
	send("")
	beforeInvalid := len(s.fake.Chat(4242).Messages)
	if _, _, err := s.fake.PressKeyboard(KeyboardPress{Row: 9}); err == nil {
		t.Fatal("missing row accepted")
	}
	if len(s.fake.Chat(4242).Messages) != beforeInvalid {
		t.Fatal("missing row created message")
	}
	snapshot := s.fake.Chat(4242)
	snapshot.ReplyKeyboard.Keyboard[0][0].Text = "changed"
	if s.fake.Chat(4242).ReplyKeyboard.Keyboard[0][0].Text != "Photo" {
		t.Fatal("snapshot aliases chat keyboard")
	}
	status, body := s.sim(http.MethodPost, "/sim/keyboard/press", map[string]any{"chat_id": 4242, "user_id": 77, "username": "bob", "row": 0, "column": 1})
	if status != 200 {
		t.Fatalf("press: %d %v", status, body)
	}
	_, updates := s.call("getUpdates", nil)
	msg := updates["result"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["text"] != "Video" || msg["from"].(map[string]any)["id"] != float64(77) {
		t.Fatalf("message: %v", msg)
	}
	// One-time means hidden, not destroyed: another press still works.
	status, _ = s.sim(http.MethodPost, "/sim/keyboard/press", map[string]any{"chat_id": 4242, "row": 0, "column": 0})
	if status != 200 {
		t.Fatal(status)
	}
	if !s.fake.Chat(4242).KeyboardHidden {
		t.Fatal("one-time keyboard was not hidden")
	}
	send(`{"force_reply":true,"input_field_placeholder":"Describe changes"}`)
	_, view := s.sim(http.MethodGet, "/sim/chat/4242", nil)
	force, ok := view["force_reply"].(map[string]any)
	if !ok || force["input_field_placeholder"] != "Describe changes" || view["reply_keyboard"] == nil {
		t.Fatalf("view: %v", view)
	}
	s.sim(http.MethodPost, "/sim/message", map[string]any{"text": "brighter", "reply_to_message_id": force["message_id"]})
	_, updates = s.call("getUpdates", nil)
	entries := updates["result"].([]any)
	reply := entries[len(entries)-1].(map[string]any)["message"].(map[string]any)
	if reply["reply_to_message"].(map[string]any)["message_id"] != force["message_id"] {
		t.Fatal(reply)
	}
	send(`{"remove_keyboard":true}`)
	before := len(s.fake.Chat(4242).Messages)
	for _, col := range []int{0, -1, 99} {
		status, _ = s.sim(http.MethodPost, "/sim/keyboard/press", map[string]any{"chat_id": 4242, "row": 0, "column": col})
		if status < 400 || status >= 500 {
			t.Fatal(status)
		}
	}
	if len(s.fake.Chat(4242).Messages) != before {
		t.Fatal("invalid press created a message")
	}
}
