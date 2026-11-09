package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

type IncomingJoinRequest struct {
	ChatID     int64          `json:"chat_id"`
	User       botapi.User    `json:"user"`
	Bio        string         `json:"bio,omitempty"`
	InviteLink map[string]any `json:"invite_link,omitempty"`
}

func (s *Server) InjectJoinRequest(in IncomingJoinRequest) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil {
		return 0, &simError{"chat not found"}
	}
	if in.User.ID <= 0 {
		return 0, &simError{"user requires a positive id"}
	}
	request := &botapi.ChatJoinRequest{Chat: chat.wire(), From: in.User, UserChatID: in.User.ID, Date: s.now().Unix(), Bio: in.Bio, InviteLink: cloneMap(in.InviteLink)}
	if s.joinRequests[in.ChatID] == nil {
		s.joinRequests[in.ChatID] = map[int64]*botapi.ChatJoinRequest{}
	}
	s.joinRequests[in.ChatID][in.User.ID] = request
	return s.pushUpdateLocked(botapi.Update{ChatJoinRequest: request}), nil
}

type simError struct{ message string }

func (e *simError) Error() string { return e.message }

func (s *Server) simJoinRequest(w http.ResponseWriter, r *http.Request) {
	var in IncomingJoinRequest
	if !readJSON(w, r, &in) {
		return
	}
	id, err := s.InjectJoinRequest(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": id})
}

func (s *Server) answerChatJoinRequest(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	userID := integerOf(params.Get("user_id"))
	if !ok || userID <= 0 {
		s.writeError(w, method, params, 400, "Bad Request: HIDE_REQUESTER_MISSING", 0)
		return
	}
	s.mu.Lock()
	chat := s.chats[chatID]
	var request *botapi.ChatJoinRequest
	if s.joinRequests[chatID] != nil {
		request = s.joinRequests[chatID][userID]
	}
	if chat == nil || request == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, "Bad Request: HIDE_REQUESTER_MISSING", 0)
		return
	}
	delete(s.joinRequests[chatID], userID)
	if strings.EqualFold(method, "approveChatJoinRequest") {
		old := chat.members[userID]
		if old == nil {
			old = map[string]any{"status": "left", "user": userWire(request.From)}
		}
		next := userMember(&request.From)
		chat.members[userID] = next
		updated := s.memberUpdatedLocked(chat, *s.botUser(), old, next, request.InviteLink)
		s.pushMembershipUpdateLocked(userID, updated)
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}
