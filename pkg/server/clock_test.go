package server

import (
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestSimulationClockControlsDatesAndExpiry(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := newStand(t, Options{BotID: 7001, Now: func() time.Time { return start }})
	if err := s.fake.ConfigureChat(ChatConfiguration{
		Chat: botapi.ChatFullInfo{"id": int64(-100), "type": "supergroup", "title": "clock"},
		Members: []botapi.ChatMember{
			{"status": "administrator", "user": map[string]any{"id": int64(7001), "is_bot": true, "first_name": "bot"}, "can_restrict_members": true},
			{"status": "member", "user": map[string]any{"id": int64(42), "is_bot": false, "first_name": "Bob"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	code, body := s.sim("GET", "/sim/clock", nil)
	if code != 200 || body["unix"] != float64(start.Unix()) {
		t.Fatalf("clock: %d %v", code, body)
	}

	until := start.Add(time.Hour).Unix()
	s.call("restrictChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"42"}, "permissions": {`{"can_send_messages":false}`}, "until_date": {strconv.FormatInt(until, 10)}})
	code, body = s.sim("POST", "/sim/clock/advance", map[string]any{"seconds": 3601})
	if code != 200 || body["unix"] != float64(start.Add(3601*time.Second).Unix()) {
		t.Fatalf("advance: %d %v", code, body)
	}
	if got := memberResult(t, s, 42)["status"]; got != "member" {
		t.Fatalf("expired status = %v", got)
	}

	code, body = s.sim("POST", "/sim/message", map[string]any{"chat_id": -100, "chat_type": "supergroup", "user_id": 42, "text": "after expiry"})
	if code != 200 {
		t.Fatalf("message: %d %v", code, body)
	}
	s.fake.mu.Lock()
	gotDate := s.fake.chats[-100].messages[len(s.fake.chats[-100].messages)-1].msg.Date
	s.fake.mu.Unlock()
	if want := start.Add(3601 * time.Second).Unix(); gotDate != want {
		t.Fatalf("message date = %d, want %d", gotDate, want)
	}
}

func TestSimulationClockSet(t *testing.T) {
	start := time.Unix(1000, 0)
	s := newStand(t, Options{Now: func() time.Time { return start }})
	code, body := s.sim("POST", "/sim/clock/set", map[string]any{"unix": 2000})
	if code != 200 || body["unix"] != float64(2000) {
		t.Fatalf("set: %d %v", code, body)
	}
}
