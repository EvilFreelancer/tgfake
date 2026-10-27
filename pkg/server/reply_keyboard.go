package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// ForceReplyView identifies the outstanding reply request in a chat.
type ForceReplyView struct {
	botapi.ForceReply
	MessageID int `json:"message_id"`
}

func cloneReplyKeyboard(k *botapi.ReplyKeyboardMarkup) *botapi.ReplyKeyboardMarkup {
	if k == nil {
		return nil
	}
	out := *k
	out.Keyboard = make([][]botapi.KeyboardButton, len(k.Keyboard))
	for i, row := range k.Keyboard {
		out.Keyboard[i] = append([]botapi.KeyboardButton{}, row...)
	}
	return &out
}

// replyControls reads the reply-keyboard side of reply_markup the way the
// Bot API server reads it (get_reply_markup): a markup is one kind only - a
// keyboard with rows, else an inline keyboard (none of these controls), else
// remove_keyboard (or its old name hide_keyboard), else force_reply. A
// keyboard whose buttons all lack text is no markup at all, as in TDLib. At
// most one result is set; parseKeyboard has refused a keyboard that cannot be
// read.
func replyControls(raw string) (keyboard *botapi.ReplyKeyboardMarkup, remove *botapi.ReplyKeyboardRemove, force *botapi.ForceReply) {
	var markup struct {
		botapi.ReplyKeyboardMarkup
		Keyboard       json.RawMessage   `json:"keyboard"`
		InlineKeyboard []json.RawMessage `json:"inline_keyboard"`
		RemoveKeyboard bool              `json:"remove_keyboard"`
		HideKeyboard   bool              `json:"hide_keyboard"`
		ForceReply     bool              `json:"force_reply"`
	}
	if json.Unmarshal([]byte(raw), &markup) != nil {
		return nil, nil, nil
	}
	read := markup.ReplyKeyboardMarkup
	rows := 0
	if markup.Keyboard != nil {
		var problem string
		if read.Keyboard, rows, problem = keyboardRows(markup.Keyboard); problem != "" {
			return nil, nil, nil
		}
	}
	switch {
	case rows > 0:
		if len(read.Keyboard) > 0 {
			return &read, nil, nil
		}
	case len(markup.InlineKeyboard) > 0:
	case markup.RemoveKeyboard || markup.HideKeyboard:
		return nil, &botapi.ReplyKeyboardRemove{RemoveKeyboard: true, Selective: read.Selective}, nil
	case markup.ForceReply:
		return nil, nil, &botapi.ForceReply{ForceReply: true, InputFieldPlaceholder: read.InputFieldPlaceholder, Selective: read.Selective}
	}
	return nil, nil, nil
}

// applyReplyMarkupLocked keeps the reply controls of a sent message's
// reply_markup on the chat and the message. It is called only after a
// successful outgoing send.
func (c *chatState) applyReplyMarkupLocked(raw string, stored *storedMessage) {
	keyboard, remove, force := replyControls(raw)
	switch {
	case keyboard != nil:
		c.replyKeyboard, c.keyboardHidden = keyboard, false
		stored.replyKeyboard = cloneReplyKeyboard(keyboard)
	case remove != nil:
		c.replyKeyboard, c.keyboardHidden = nil, false
		stored.removeKeyboard = remove
	case force != nil:
		c.forceReply = &ForceReplyView{ForceReply: *force, MessageID: stored.msg.MessageID}
		stored.forceReply = copyPointer(c.forceReply)
	}
}

// keyboardRows reads the keyboard field of reply_markup the way the Bot API
// server does (get_array, get_keyboard_button): an array of rows, a null row
// read as an empty one, and a button that is its text or an object with a
// text, a number counting as text. TDLib then drops the buttons without text
// and the rows left empty. lines is the number of rows the field had, which
// makes the markup a keyboard; problem is the Bot API's refusal.
func keyboardRows(raw json.RawMessage) (keyboard [][]botapi.KeyboardButton, lines int, problem string) {
	var rows []json.RawMessage
	if isNull(raw) || json.Unmarshal(raw, &rows) != nil {
		return nil, 0, `Bad Request: field "keyboard" must be of type Array`
	}
	for _, row := range rows {
		var buttons []json.RawMessage
		if !isNull(row) && json.Unmarshal(row, &buttons) != nil {
			return nil, 0, "Bad Request: expected an Array of KeyboardButton"
		}
		var kept []botapi.KeyboardButton
		for _, button := range buttons {
			text, problem := keyboardButtonText(button)
			if problem != "" {
				return nil, 0, "Bad Request: can't parse KeyboardButton: " + problem
			}
			if text != "" {
				kept = append(kept, botapi.KeyboardButton{Text: text})
			}
		}
		if len(kept) > 0 {
			keyboard = append(keyboard, kept)
		}
	}
	return keyboard, len(rows), ""
}

// keyboardButtonText is the text of one reply button, or td's refusal of it.
func keyboardButtonText(raw json.RawMessage) (text, problem string) {
	if isNull(raw) {
		return "", "KeyboardButton must be a String or an Object"
	}
	if json.Unmarshal(raw, &text) == nil {
		return text, ""
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return "", "KeyboardButton must be a String or an Object"
	}
	value, ok := object["text"]
	if !ok {
		return "", `Can't find field "text"`
	}
	var number json.Number
	switch {
	case isNull(value):
	case json.Unmarshal(value, &text) == nil:
		return text, ""
	case json.Unmarshal(value, &number) == nil:
		return number.String(), ""
	}
	return "", `Field "text" must be of type String`
}

func isNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// KeyboardPress selects a zero-based row and column of the active reply menu.
type KeyboardPress struct {
	ChatID    int64  `json:"chat_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	Row       int    `json:"row"`
	Column    int    `json:"column"`
}

// PressKeyboard sends an ordinary message as the selected user. A hidden
// one-time menu can be reopened; only ReplyKeyboardRemove destroys it.
func (s *Server) PressKeyboard(in KeyboardPress) (updateID, messageID int, err error) {
	message := IncomingMessage{ChatID: in.ChatID, UserID: in.UserID, Username: in.Username, FirstName: in.FirstName}
	message.normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chats[message.ChatID]
	if c == nil || c.replyKeyboard == nil {
		return 0, 0, fmt.Errorf("chat %d has no reply keyboard", message.ChatID)
	}
	rows := c.replyKeyboard.Keyboard
	if in.Row < 0 || in.Row >= len(rows) || in.Column < 0 || in.Column >= len(rows[in.Row]) {
		return 0, 0, fmt.Errorf("reply button row %d column %d does not exist", in.Row, in.Column)
	}
	message.Text = rows[in.Row][in.Column].Text
	if c.forceReply != nil {
		message.ReplyToMessageID = c.forceReply.MessageID
	}
	if c.replyKeyboard.OneTimeKeyboard {
		c.keyboardHidden = true
	}
	updateID, messageID = s.injectMessageLocked(message)
	return updateID, messageID, nil
}

func (s *Server) simKeyboardPress(w http.ResponseWriter, r *http.Request) {
	var in KeyboardPress
	if !readJSON(w, r, &in) {
		return
	}
	updateID, messageID, err := s.PressKeyboard(in)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": updateID, "message_id": messageID})
}
