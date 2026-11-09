package server

import (
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestIncomingEditAndReaction(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.SetAllowedUpdates([]string{"edited_message", "message_reaction"})
	_, messageID := s.fake.InjectMessage(IncomingMessage{Text: "before"})
	code, body := s.sim("POST", "/sim/message/edit", IncomingMessageEdit{ChatID: 4242, MessageID: messageID, Text: "after"})
	if code != 200 {
		t.Fatalf("edit: %d %v", code, body)
	}
	code, body = s.sim("POST", "/sim/reaction", IncomingReaction{ChatID: 4242, MessageID: messageID, User: &botapi.User{ID: 4242, FirstName: "Alice"}, NewReaction: []map[string]any{{"type": "emoji", "emoji": "👍"}}})
	if code != 200 {
		t.Fatalf("reaction: %d %v", code, body)
	}
	s.fake.mu.Lock()
	updates := append([]botapi.Update(nil), s.fake.pending...)
	s.fake.mu.Unlock()
	if len(updates) != 2 || updates[0].EditedMessage == nil || updates[0].EditedMessage.Text != "after" || updates[1].MessageReaction == nil {
		t.Fatalf("updates: %#v", updates)
	}
}
