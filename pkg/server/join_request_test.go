package server

import (
	"net/url"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestJoinRequestApproveAndDecline(t *testing.T) {
	s := moderationStand(t)
	s.fake.SetAllowedUpdates([]string{"chat_join_request", "chat_member"})
	code, body := s.sim("POST", "/sim/join-request", IncomingJoinRequest{ChatID: -100, User: botapi.User{ID: 77, FirstName: "Applicant"}, Bio: "hello"})
	if code != 200 {
		t.Fatalf("inject: %d %v", code, body)
	}
	s.fake.mu.Lock()
	first := s.fake.pending[0]
	s.fake.mu.Unlock()
	if first.ChatJoinRequest == nil || first.ChatJoinRequest.Bio != "hello" {
		t.Fatalf("request: %#v", first)
	}
	code, body = s.call("approveChatJoinRequest", url.Values{"chat_id": {"-100"}, "user_id": {"77"}})
	if code != 200 || memberResult(t, s, 77)["status"] != "member" {
		t.Fatalf("approve: %d %v", code, body)
	}
	code, body = s.call("approveChatJoinRequest", url.Values{"chat_id": {"-100"}, "user_id": {"77"}})
	if code != 400 || body["description"] != "Bad Request: HIDE_REQUESTER_MISSING" {
		t.Fatalf("missing: %d %v", code, body)
	}

	s.sim("POST", "/sim/join-request", IncomingJoinRequest{ChatID: -100, User: botapi.User{ID: 78, FirstName: "Declined"}})
	code, _ = s.call("declineChatJoinRequest", url.Values{"chat_id": {"-100"}, "user_id": {"78"}})
	if code != 200 {
		t.Fatalf("decline = %d", code)
	}
}
