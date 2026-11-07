package server

import (
	"fmt"
	"net/http"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

type MemberLifecycle struct {
	ChatID     int64           `json:"chat_id"`
	Action     string          `json:"action"`
	User       botapi.User     `json:"user"`
	InviteLink map[string]any  `json:"invite_link,omitempty"`
	Rights     map[string]bool `json:"rights,omitempty"`
}

func (s *Server) InjectMemberLifecycle(in MemberLifecycle) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil {
		return 0, 0, fmt.Errorf("chat %d not found", in.ChatID)
	}
	if in.User.ID <= 0 {
		return 0, 0, fmt.Errorf("user requires a positive id")
	}
	if in.User.FirstName == "" {
		in.User.FirstName = defaultFirstName
	}
	old := cloneMap(chat.members[in.User.ID])
	if old == nil {
		old = map[string]any{"status": "left", "user": userWire(in.User)}
	}
	var next map[string]any
	switch in.Action {
	case "join":
		next = userMember(&in.User)
	case "leave":
		next = map[string]any{"status": "left", "user": userWire(in.User)}
	case "promote":
		next = map[string]any{"status": "administrator", "user": userWire(in.User), "can_be_edited": true}
		for _, field := range administratorFields {
			next[field] = in.Rights[field]
		}
	default:
		return 0, 0, fmt.Errorf("action is join, leave, or promote")
	}
	chat.members[in.User.ID] = next
	messageID := 0
	if in.Action == "join" || in.Action == "leave" {
		msg := &botapi.Message{From: &in.User, Chat: chat.wire(), Date: s.now().Unix()}
		if in.Action == "join" {
			msg.NewChatMembers = []botapi.User{in.User}
		} else {
			msg.LeftChatMember = &in.User
		}
		stored := chat.appendLocked(msg, false, "", false)
		messageID = stored.msg.MessageID
		s.pushUpdateLocked(botapi.Update{Message: stored.clone()})
	}
	updated := s.memberUpdatedLocked(chat, in.User, old, next, in.InviteLink)
	return s.pushMembershipUpdateLocked(in.User.ID, updated), messageID, nil
}

func userWire(user botapi.User) map[string]any {
	return map[string]any{"id": user.ID, "is_bot": user.IsBot, "first_name": user.FirstName, "last_name": user.LastName, "username": user.Username}
}

func (s *Server) memberUpdatedLocked(chat *chatState, actor botapi.User, old, next map[string]any, invite map[string]any) *botapi.ChatMemberUpdated {
	return &botapi.ChatMemberUpdated{Chat: chat.wire(), From: actor, Date: s.now().Unix(), OldChatMember: cloneMap(old), NewChatMember: cloneMap(next), InviteLink: cloneMap(invite)}
}

func (s *Server) pushMembershipUpdateLocked(userID int64, updated *botapi.ChatMemberUpdated) int {
	if userID == s.opts.BotID {
		return s.pushUpdateLocked(botapi.Update{MyChatMember: updated})
	}
	return s.pushUpdateLocked(botapi.Update{ChatMember: updated})
}

func (s *Server) simMemberLifecycle(w http.ResponseWriter, r *http.Request) {
	var in MemberLifecycle
	if !readJSON(w, r, &in) {
		return
	}
	updateID, messageID, err := s.InjectMemberLifecycle(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": updateID, "message_id": messageID})
}
