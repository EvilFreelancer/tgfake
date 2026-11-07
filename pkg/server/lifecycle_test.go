package server

import (
	"net/url"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestMemberLifecycleUpdatesAndServiceMessages(t *testing.T) {
	s := moderationStand(t)
	s.fake.SetAllowedUpdates([]string{"message", "chat_member", "my_chat_member"})
	code, body := s.sim("POST", "/sim/member", MemberLifecycle{ChatID: -100, Action: "join", User: botapi.User{ID: 55, FirstName: "Jane"}})
	if code != 200 || body["message_id"] == float64(0) {
		t.Fatalf("join: %d %v", code, body)
	}
	if memberResult(t, s, 55)["status"] != "member" {
		t.Fatal("join did not add member")
	}
	s.fake.mu.Lock()
	updates := append([]botapi.Update(nil), s.fake.pending...)
	s.fake.mu.Unlock()
	if len(updates) != 2 || updates[0].Message == nil || len(updates[0].Message.NewChatMembers) != 1 || updates[1].ChatMember == nil {
		t.Fatalf("join updates: %#v", updates)
	}
	s.sim("POST", "/sim/member", MemberLifecycle{ChatID: -100, Action: "leave", User: botapi.User{ID: 55, FirstName: "Jane"}})
	if memberResult(t, s, 55)["status"] != "left" {
		t.Fatal("leave did not mark left")
	}
	s.sim("POST", "/sim/member", MemberLifecycle{ChatID: -100, Action: "promote", User: botapi.User{ID: 7001, IsBot: true, FirstName: "tgfake"}, Rights: map[string]bool{"can_restrict_members": true}})
	s.fake.mu.Lock()
	last := s.fake.pending[len(s.fake.pending)-1]
	s.fake.mu.Unlock()
	if last.MyChatMember == nil {
		t.Fatalf("bot transition was not my_chat_member: %#v", last)
	}
}

func TestModerationEmitsSubscribedChatMemberUpdate(t *testing.T) {
	s := moderationStand(t)
	s.fake.SetAllowedUpdates([]string{"chat_member"})
	s.call("banChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}})
	s.fake.mu.Lock()
	defer s.fake.mu.Unlock()
	if len(s.fake.pending) != 1 || s.fake.pending[0].ChatMember == nil {
		t.Fatalf("pending: %#v", s.fake.pending)
	}
}
