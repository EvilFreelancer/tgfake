package server

import (
	"net/url"
	"strconv"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestGroupMigration(t *testing.T) {
	s := newStand(t, Options{BotID: 7001})
	if err := s.fake.ConfigureChat(ChatConfiguration{Chat: botapi.ChatFullInfo{"id": int64(-10), "type": "group", "title": "Legacy"}, Members: []botapi.ChatMember{{"status": "member", "user": map[string]any{"id": int64(42), "first_name": "Bob", "is_bot": false}}}}); err != nil {
		t.Fatal(err)
	}
	code, body := s.sim("POST", "/sim/chat/migrate", ChatMigration{ChatID: -10, NewChatID: -10010})
	if code != 200 {
		t.Fatalf("migrate: %d %v", code, body)
	}
	if member := memberResultForChat(t, s, -10010, 42); member["status"] != "member" {
		t.Fatalf("member: %#v", member)
	}
	code, body = s.call("getChat", url.Values{"chat_id": {"-10"}})
	if code != 400 || body["description"] != "Bad Request: group chat was upgraded to a supergroup chat" || body["parameters"].(map[string]any)["migrate_to_chat_id"] != float64(-10010) {
		t.Fatalf("old chat: %d %v", code, body)
	}
	s.fake.mu.Lock()
	updates := append([]botapi.Update(nil), s.fake.pending...)
	s.fake.mu.Unlock()
	if len(updates) != 2 || updates[0].Message.MigrateToChatID != -10010 || updates[1].Message.MigrateFromChatID != -10 {
		t.Fatalf("updates: %#v", updates)
	}
}

func memberResultForChat(t *testing.T, s *stand, chatID, userID int64) map[string]any {
	t.Helper()
	code, body := s.call("getChatMember", url.Values{"chat_id": {strconv.FormatInt(chatID, 10)}, "user_id": {strconv.FormatInt(userID, 10)}})
	if code != 200 {
		t.Fatalf("member: %d %v", code, body)
	}
	return result(t, body)
}
