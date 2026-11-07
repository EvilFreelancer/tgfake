package server

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func moderationStand(t *testing.T) *stand {
	t.Helper()
	s := newStand(t, Options{BotID: 7001})
	err := s.fake.ConfigureChat(ChatConfiguration{
		Chat: botapi.ChatFullInfo{"id": int64(-100), "type": "supergroup", "title": "mods"},
		Members: []botapi.ChatMember{
			{"status": "administrator", "user": map[string]any{"id": int64(7001), "is_bot": true, "first_name": "tgfake"}, "can_restrict_members": true, "can_promote_members": true},
			{"status": "member", "user": map[string]any{"id": int64(42), "is_bot": false, "first_name": "Bob"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func memberResult(t *testing.T, s *stand, userID int64) map[string]any {
	t.Helper()
	code, body := s.call("getChatMember", url.Values{"chat_id": {"-100"}, "user_id": {fmt.Sprint(userID)}})
	if code != 200 {
		t.Fatalf("getChatMember: %d %v", code, body)
	}
	return result(t, body)
}

func TestModerationMutations(t *testing.T) {
	s := moderationStand(t)
	until := time.Now().Add(time.Hour).Unix()
	code, body := s.call("restrictChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "permissions": {`{"can_send_messages":false}`}, "until_date": {fmt.Sprint(until)}})
	if code != 200 || body["result"] != true {
		t.Fatalf("restrict: %d %v", code, body)
	}
	member := memberResult(t, s, 42)
	if member["status"] != "restricted" || member["can_send_messages"] != false || member["until_date"] != float64(until) {
		t.Fatalf("restricted member: %#v", member)
	}

	all := url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "use_independent_chat_permissions": {"true"}}
	for _, field := range permissionFields {
		all.Set(field, "true")
	}
	s.call("restrictChatMember", all)
	if got := memberResult(t, s, 42)["status"]; got != "member" {
		t.Fatalf("unrestrict status = %v", got)
	}

	s.call("banChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "until_date": {fmt.Sprint(time.Now().Add(10 * time.Second).Unix())}, "revoke_messages": {"true"}})
	if member := memberResult(t, s, 42); member["status"] != "kicked" || member["until_date"] != float64(0) {
		t.Fatalf("banned member: %#v", member)
	}
	s.call("unbanChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "only_if_banned": {"true"}})
	if got := memberResult(t, s, 42)["status"]; got != "left" {
		t.Fatalf("unban status = %v", got)
	}
}

func TestPromoteDemotePermissionsAndTitle(t *testing.T) {
	s := moderationStand(t)
	s.call("promoteChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "can_delete_messages": {"true"}})
	if member := memberResult(t, s, 42); member["status"] != "administrator" || member["can_delete_messages"] != true {
		t.Fatalf("administrator: %#v", member)
	}
	s.call("setChatAdministratorCustomTitle", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "custom_title": {"Guardian"}})
	if got := memberResult(t, s, 42)["custom_title"]; got != "Guardian" {
		t.Fatalf("title = %v", got)
	}
	s.call("promoteChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}})
	if got := memberResult(t, s, 42)["status"]; got != "member" {
		t.Fatalf("demote status = %v", got)
	}

	s.call("setChatPermissions", url.Values{"chat_id": {"-100"}, "permissions": {`{"can_send_messages":true}`}})
	code, body := s.call("getChat", url.Values{"chat_id": {"-100"}})
	permissions := result(t, body)["permissions"].(map[string]any)
	if code != 200 || permissions["can_send_photos"] != true {
		t.Fatalf("permissions: %d %#v", code, permissions)
	}
}

func TestModerationEnforcesRightsAndProtectedTargets(t *testing.T) {
	s := moderationStand(t)
	for _, tc := range []struct {
		name, method, userID, want string
	}{
		{"self", "banChatMember", "7001", "Bad Request: PARTICIPANT_ID_INVALID"},
		{"administrator", "banChatMember", "99", "Bad Request: user is an administrator of the chat"},
		{"creator", "restrictChatMember", "100", "Bad Request: can't remove chat owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.userID != "7001" {
				s.fake.mu.Lock()
				status := "administrator"
				if tc.userID == "100" {
					status = "creator"
				}
				s.fake.chats[-100].members[integerOf(tc.userID)] = map[string]any{"status": status, "user": map[string]any{"id": integerOf(tc.userID), "is_bot": false, "first_name": "Protected"}}
				s.fake.mu.Unlock()
			}
			code, body := s.call(tc.method, url.Values{"chat_id": {"-100"}, "user_id": {tc.userID}, "permissions": {`{"can_send_messages":false}`}})
			if code != 400 || body["description"] != tc.want {
				t.Fatalf("got %d %v", code, body)
			}
		})
	}

	s.fake.mu.Lock()
	s.fake.chats[-100].members[7001]["can_restrict_members"] = false
	s.fake.mu.Unlock()
	code, body := s.call("banChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}})
	if code != 400 || body["description"] != "Bad Request: not enough rights to restrict/unrestrict chat member" {
		t.Fatalf("rights: %d %v", code, body)
	}
}

func TestSimMessageEnforcesMembershipAndRestrictionExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s := moderationStand(t)
	s.fake.now = func() time.Time { return now }
	s.call("restrictChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "permissions": {`{"can_send_messages":false}`}, "until_date": {fmt.Sprint(now.Add(time.Hour).Unix())}})
	code, body := s.sim("POST", "/sim/message", map[string]any{"chat_id": -100, "chat_type": "supergroup", "user_id": 42, "text": "blocked"})
	if code != 403 || body["error"] != "sender is restricted from sending messages" {
		t.Fatalf("restricted: %d %v", code, body)
	}
	now = now.Add(2 * time.Hour)
	code, body = s.sim("POST", "/sim/message", map[string]any{"chat_id": -100, "chat_type": "supergroup", "user_id": 42, "text": "allowed"})
	if code != 200 {
		t.Fatalf("expired restriction: %d %v", code, body)
	}
	s.call("banChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}})
	code, body = s.sim("POST", "/sim/message", map[string]any{"chat_id": -100, "chat_type": "supergroup", "user_id": 42, "text": "blocked"})
	if code != 403 || body["error"] != "sender is not a member of the chat" {
		t.Fatalf("kicked: %d %v", code, body)
	}
}
