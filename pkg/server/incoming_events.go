package server

import (
	"fmt"
	"net/http"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

type IncomingMessageEdit struct {
	ChatID    int64                  `json:"chat_id"`
	MessageID int                    `json:"message_id"`
	Text      string                 `json:"text"`
	Entities  []botapi.MessageEntity `json:"entities,omitempty"`
}

func (s *Server) EditIncomingMessage(in IncomingMessageEdit) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil || chat.byID[in.MessageID] == nil || chat.byID[in.MessageID].deleted {
		return 0, fmt.Errorf("message not found")
	}
	stored := chat.byID[in.MessageID]
	stored.msg.Text, stored.msg.Entities, stored.msg.EditDate = in.Text, append([]botapi.MessageEntity(nil), in.Entities...), s.now().Unix()
	stored.edited = true
	return s.pushUpdateLocked(botapi.Update{EditedMessage: stored.clone()}), nil
}

type IncomingReaction struct {
	ChatID      int64            `json:"chat_id"`
	MessageID   int              `json:"message_id"`
	User        *botapi.User     `json:"user,omitempty"`
	ActorChat   *botapi.Chat     `json:"actor_chat,omitempty"`
	OldReaction []map[string]any `json:"old_reaction"`
	NewReaction []map[string]any `json:"new_reaction"`
}

func (s *Server) InjectMessageReaction(in IncomingReaction) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil || chat.byID[in.MessageID] == nil {
		return 0, fmt.Errorf("message not found")
	}
	reaction := &botapi.MessageReactionUpdated{Chat: chat.wire(), MessageID: in.MessageID, User: in.User, ActorChat: in.ActorChat, Date: s.now().Unix(), OldReaction: in.OldReaction, NewReaction: in.NewReaction}
	return s.pushUpdateLocked(botapi.Update{MessageReaction: reaction}), nil
}

func (s *Server) simEditIncomingMessage(w http.ResponseWriter, r *http.Request) {
	var in IncomingMessageEdit
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.EditIncomingMessage(in)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": id})
}

func (s *Server) simMessageReaction(w http.ResponseWriter, r *http.Request) {
	var in IncomingReaction
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.InjectMessageReaction(in)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": id})
}
