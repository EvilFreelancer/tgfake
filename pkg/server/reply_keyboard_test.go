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

func TestKeyboardPressUsesKeyboardAndForceReply(t *testing.T) {
	s := newStand(t, Options{})
	send := func(markup string) {
		t.Helper()
		status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}})
		if status != 200 {
			t.Fatalf("send: %d %v", status, body)
		}
	}
	send(`{"keyboard":[[{"text":"Photo"}]],"one_time_keyboard":true}`)
	s.sim(http.MethodPost, "/sim/message", map[string]any{"text": "typed"})
	if s.fake.Chat(4242).KeyboardHidden {
		t.Fatal("one-time keyboard hid without being used")
	}
	send(`{"force_reply":true}`)
	forceID := s.fake.Chat(4242).ForceReply.MessageID
	status, body := s.sim(http.MethodPost, "/sim/keyboard/press", map[string]any{"row": 0, "column": 0})
	if status != 200 {
		t.Fatalf("press: %d %v", status, body)
	}
	chat := s.fake.Chat(4242)
	if !chat.KeyboardHidden {
		t.Fatal("one-time keyboard did not hide after its button was pressed")
	}
	if chat.ForceReply != nil {
		t.Fatal("button press did not clear ForceReply")
	}
	_, updates := s.call("getUpdates", nil)
	entries := updates["result"].([]any)
	pressed := entries[len(entries)-1].(map[string]any)["message"].(map[string]any)
	reply, ok := pressed["reply_to_message"].(map[string]any)
	if !ok || reply["message_id"] != float64(forceID) {
		t.Fatalf("button press is not a reply to ForceReply message %d: %v", forceID, pressed)
	}
}

// reply_markup is read the way the Bot API server reads it: a reply button may
// be just its text, and a markup is one kind only - a keyboard with rows, else
// an inline keyboard, else remove_keyboard (or hide_keyboard), else force_reply.
func TestReplyMarkupReadsLikeTheBotAPIServer(t *testing.T) {
	s := newStand(t, Options{})
	send := func(markup string) {
		t.Helper()
		if status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}}); status != 200 {
			t.Fatalf("send %s: %d %v", markup, status, body)
		}
	}
	send(`{"keyboard":[["Yes",{"text":"No"}]],"one_time_keyboard":true}`)
	v := s.fake.Chat(4242)
	if v.ReplyKeyboard == nil || len(v.ReplyKeyboard.Keyboard) != 1 || v.ReplyKeyboard.Keyboard[0][0].Text != "Yes" || v.ReplyKeyboard.Keyboard[0][1].Text != "No" || !v.ReplyKeyboard.OneTimeKeyboard {
		t.Fatalf("string buttons: %+v", v.ReplyKeyboard)
	}
	send(`{"keyboard":[["Again"]],"remove_keyboard":true}`)
	if v = s.fake.Chat(4242); v.ReplyKeyboard == nil || v.ReplyKeyboard.Keyboard[0][0].Text != "Again" {
		t.Fatalf("a keyboard with rows wins over remove_keyboard: %+v", v.ReplyKeyboard)
	}
	send(`{"inline_keyboard":[[{"text":"Go","callback_data":"go"}]],"force_reply":true}`)
	if v = s.fake.Chat(4242); v.ForceReply != nil {
		t.Fatalf("an inline keyboard wins over force_reply: %+v", v.ForceReply)
	}
	send(`{"keyboard":[],"force_reply":true}`)
	if v = s.fake.Chat(4242); v.ForceReply == nil || v.ReplyKeyboard.Keyboard[0][0].Text != "Again" {
		t.Fatalf("an empty keyboard is no keyboard: %+v %+v", v.ReplyKeyboard, v.ForceReply)
	}
	send(`{"hide_keyboard":true}`)
	if v = s.fake.Chat(4242); v.ReplyKeyboard != nil {
		t.Fatalf("hide_keyboard keeps the keyboard: %+v", v.ReplyKeyboard)
	}
}

// A reply keyboard the Bot API server cannot parse is refused in its words
// (telegram-bot-api get_reply_markup, get_array, get_keyboard_button), and
// TDLib drops buttons without text and rows left empty.
func TestReplyKeyboardRefusalsAndEmptyButtons(t *testing.T) {
	s := newStand(t, Options{})
	refusals := map[string]string{
		`{"keyboard":"x"}`:               `Bad Request: field "keyboard" must be of type Array`,
		`{"keyboard":["x"]}`:             `Bad Request: expected an Array of KeyboardButton`,
		`{"keyboard":[[1]]}`:             `Bad Request: can't parse KeyboardButton: KeyboardButton must be a String or an Object`,
		`{"keyboard":[[{}]]}`:            `Bad Request: can't parse KeyboardButton: Can't find field "text"`,
		`{"keyboard":[[{"text":true}]]}`: `Bad Request: can't parse KeyboardButton: Field "text" must be of type String`,
	}
	for markup, want := range refusals {
		status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}})
		if status != 400 || body["description"] != want {
			t.Errorf("%s: %d %v", markup, status, body)
		}
	}
	if messages := s.fake.Chat(4242).Messages; len(messages) != 0 {
		t.Fatalf("a refused send was kept: %+v", messages)
	}
	send := func(markup string) {
		t.Helper()
		if status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}}); status != 200 {
			t.Fatalf("send %s: %d %v", markup, status, body)
		}
	}
	send(`{"keyboard":[[{"text":""},"Yes"],[],null,[""]]}`)
	if v := s.fake.Chat(4242); v.ReplyKeyboard == nil || len(v.ReplyKeyboard.Keyboard) != 1 || len(v.ReplyKeyboard.Keyboard[0]) != 1 {
		t.Fatalf("empty buttons and rows: %+v", v.ReplyKeyboard)
	}
	send(`{"keyboard":[[""]],"force_reply":true}`)
	if v := s.fake.Chat(4242); v.ReplyKeyboard.Keyboard[0][0].Text != "Yes" || v.ForceReply != nil {
		t.Fatalf("a keyboard of empty buttons is no markup at all: %+v %+v", v.ReplyKeyboard, v.ForceReply)
	}
}

// A keyboard with rows is the whole markup: an inline keyboard sent beside it
// is not kept on the message, even when TDLib then drops every reply button.
func TestReplyKeyboardWinsOverInlineKeyboard(t *testing.T) {
	s := newStand(t, Options{})
	for i, keyboard := range []string{`[["A"]]`, `[[""]]`} {
		status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {`{"keyboard":` + keyboard + `,"inline_keyboard":[[{"text":"B","callback_data":"b"}]]}`}})
		if status != 200 || result(t, body)["reply_markup"] != nil {
			t.Fatalf("%s: %d %v", keyboard, status, body)
		}
		if m := s.fake.Chat(4242).Messages[i]; len(m.Keyboard) != 0 {
			t.Fatalf("%s: the message kept the inline keyboard: %+v", keyboard, m.Keyboard)
		}
	}
	if v := s.fake.Chat(4242); v.ReplyKeyboard == nil || v.ReplyKeyboard.Keyboard[0][0].Text != "A" {
		t.Fatalf("reply keyboard: %+v", v.ReplyKeyboard)
	}
}

// Deleting the message whose reply keyboard or ForceReply the chat shows takes
// that control away, as TDLib clears a chat's reply markup with its message.
func TestDeletingTheMessageTakesItsReplyControlAway(t *testing.T) {
	s := newStand(t, Options{})
	for _, markup := range []string{`{"keyboard":[["A"]]}`, `{"force_reply":true}`, ``} {
		if status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}}); status != 200 {
			t.Fatalf("send %s: %d %v", markup, status, body)
		}
	}
	s.call("deleteMessage", url.Values{"chat_id": {"4242"}, "message_id": {"3"}})
	if v := s.fake.Chat(4242); v.ReplyKeyboard == nil || v.ForceReply == nil {
		t.Fatalf("an unrelated deletion took a control away: %+v %+v", v.ReplyKeyboard, v.ForceReply)
	}
	s.call("deleteMessage", url.Values{"chat_id": {"4242"}, "message_id": {"2"}})
	if v := s.fake.Chat(4242); v.ForceReply != nil || v.ReplyKeyboard == nil {
		t.Fatalf("after deleting the ForceReply: %+v %+v", v.ReplyKeyboard, v.ForceReply)
	}
	s.call("deleteMessages", url.Values{"chat_id": {"4242"}, "message_ids": {"[1]"}})
	if v := s.fake.Chat(4242); v.ReplyKeyboard != nil || v.KeyboardHidden {
		t.Fatalf("after deleting the keyboard: %+v hidden %v", v.ReplyKeyboard, v.KeyboardHidden)
	}
	if status, _ := s.sim("POST", "/sim/keyboard/press", map[string]any{"row": 0, "column": 0}); status != 404 {
		t.Fatalf("a press after the deletion: %d", status)
	}
}
