package server

import (
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestIncomingSenderChatMessages(t *testing.T) {
	s := newStand(t, Options{})
	group := &botapi.Chat{ID: -100, Type: "supergroup", Title: "Group"}
	s.fake.InjectMessage(IncomingMessage{ChatID: -100, ChatType: "supergroup", Text: "anonymous", SenderChat: group})
	channel := &botapi.Chat{ID: -200, Type: "channel", Title: "News"}
	s.fake.InjectMessage(IncomingMessage{ChatID: -100, ChatType: "supergroup", Text: "linked", SenderChat: channel, IsAutomaticForward: true})
	s.fake.mu.Lock()
	updates := append([]botapi.Update(nil), s.fake.pending...)
	s.fake.mu.Unlock()
	if updates[0].Message.From == nil || updates[0].Message.From.ID != 1087968824 || updates[0].Message.SenderChat.ID != -100 {
		t.Fatalf("anonymous: %#v", updates[0].Message)
	}
	if updates[1].Message.From != nil || updates[1].Message.SenderChat.ID != -200 || !updates[1].Message.IsAutomaticForward {
		t.Fatalf("linked: %#v", updates[1].Message)
	}
}
