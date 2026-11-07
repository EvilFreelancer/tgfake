package server

import (
	"fmt"
	"net/http"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

type ChatMigration struct {
	ChatID    int64 `json:"chat_id"`
	NewChatID int64 `json:"new_chat_id"`
}

func (s *Server) MigrateChat(in ChatMigration) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.chats[in.ChatID]
	if old == nil || old.typ != "group" {
		return 0, 0, fmt.Errorf("source must be an existing group")
	}
	if in.NewChatID == 0 || in.NewChatID == in.ChatID || s.chats[in.NewChatID] != nil {
		return 0, 0, fmt.Errorf("new_chat_id must be unused and non-zero")
	}
	newChat := s.ensureChatLocked(in.NewChatID, "supergroup", old.title, nil)
	newChat.info = cloneMap(old.info)
	newChat.info["id"], newChat.info["type"] = in.NewChatID, "supergroup"
	newChat.members = map[int64]map[string]any{}
	for id, member := range old.members {
		newChat.members[id] = cloneMap(member)
	}
	s.migrations[in.ChatID] = in.NewChatID
	oldMessage := &botapi.Message{From: s.botUser(), Chat: old.wire(), Date: s.now().Unix(), MigrateToChatID: in.NewChatID}
	newMessage := &botapi.Message{From: s.botUser(), Chat: newChat.wire(), Date: s.now().Unix(), MigrateFromChatID: in.ChatID}
	oldStored, newStored := old.appendLocked(oldMessage, true, "", false), newChat.appendLocked(newMessage, true, "", false)
	oldUpdate := s.pushUpdateLocked(botapi.Update{Message: oldStored.clone()})
	newUpdate := s.pushUpdateLocked(botapi.Update{Message: newStored.clone()})
	return oldUpdate, newUpdate, nil
}

func (s *Server) simMigrateChat(w http.ResponseWriter, r *http.Request) {
	var in ChatMigration
	if !readJSON(w, r, &in) {
		return
	}
	oldID, newID, err := s.MigrateChat(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"old_update_id": oldID, "new_update_id": newID})
}
