package server

import (
	"encoding/json"
	"fmt"
	"net/http"

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

// applyReplyMarkupLocked keeps the reply-keyboard side of a sent message's
// reply_markup, read the way the Bot API server reads it: a button is an
// object with its text or the text alone, and a markup is one kind only - a
// keyboard with rows, else an inline keyboard (none of these controls), else
// remove_keyboard (or its old name hide_keyboard), else force_reply. It is
// called only after a successful outgoing send.
func (c *chatState) applyReplyMarkupLocked(raw string, stored *storedMessage) {
	var markup struct {
		botapi.ReplyKeyboardMarkup
		Keyboard       [][]json.RawMessage `json:"keyboard"`
		InlineKeyboard []json.RawMessage   `json:"inline_keyboard"`
		RemoveKeyboard bool                `json:"remove_keyboard"`
		HideKeyboard   bool                `json:"hide_keyboard"`
		ForceReply     bool                `json:"force_reply"`
	}
	if json.Unmarshal([]byte(raw), &markup) != nil {
		return
	}
	keyboard := markup.ReplyKeyboardMarkup
	for _, row := range markup.Keyboard {
		buttons := make([]botapi.KeyboardButton, 0, len(row))
		for _, b := range row {
			var button botapi.KeyboardButton
			if json.Unmarshal(b, &button.Text) != nil && json.Unmarshal(b, &button) != nil {
				return
			}
			buttons = append(buttons, button)
		}
		keyboard.Keyboard = append(keyboard.Keyboard, buttons)
	}
	switch {
	case len(keyboard.Keyboard) > 0:
		c.replyKeyboard, c.keyboardHidden = &keyboard, false
		stored.replyKeyboard = cloneReplyKeyboard(&keyboard)
	case len(markup.InlineKeyboard) > 0:
		// An inline keyboard leaves the reply keyboard and ForceReply alone.
	case markup.RemoveKeyboard || markup.HideKeyboard:
		c.replyKeyboard, c.keyboardHidden = nil, false
		stored.removeKeyboard = &botapi.ReplyKeyboardRemove{RemoveKeyboard: true, Selective: keyboard.Selective}
	case markup.ForceReply:
		force := botapi.ForceReply{ForceReply: true, InputFieldPlaceholder: keyboard.InputFieldPlaceholder, Selective: keyboard.Selective}
		c.forceReply = &ForceReplyView{ForceReply: force, MessageID: stored.msg.MessageID}
		stored.forceReply = copyPointer(c.forceReply)
	}
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
