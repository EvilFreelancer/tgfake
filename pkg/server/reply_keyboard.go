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

// applyReplyMarkupLocked is called only after a successful outgoing send.
func (c *chatState) applyReplyMarkupLocked(raw string, stored *storedMessage) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return
	}
	if _, ok := fields["keyboard"]; ok {
		var k botapi.ReplyKeyboardMarkup
		if json.Unmarshal([]byte(raw), &k) == nil {
			c.replyKeyboard = &k
			c.keyboardHidden = false
			stored.replyKeyboard = cloneReplyKeyboard(&k)
		}
	}
	var remove botapi.ReplyKeyboardRemove
	if json.Unmarshal([]byte(raw), &remove) == nil && remove.RemoveKeyboard {
		c.replyKeyboard = nil
		c.keyboardHidden = false
		stored.removeKeyboard = &remove
	}
	var force botapi.ForceReply
	if json.Unmarshal([]byte(raw), &force) == nil && force.ForceReply {
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
