package server

import (
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestIncomingForwardOriginAndEntities(t *testing.T) {
	s := newStand(t, Options{})
	origins := []*botapi.MessageOrigin{
		{Type: "user", Date: 1, SenderUser: &botapi.User{ID: 10, FirstName: "User"}},
		{Type: "hidden_user", Date: 2, SenderUserName: "Hidden"},
		{Type: "chat", Date: 3, SenderChat: &botapi.Chat{ID: -10, Type: "group", Title: "Chat"}},
		{Type: "channel", Date: 4, Chat: &botapi.Chat{ID: -20, Type: "channel", Title: "Channel"}, MessageID: 9},
	}
	for _, origin := range origins {
		s.fake.InjectMessage(IncomingMessage{Text: "link @user emoji", ForwardOrigin: origin, Entities: []botapi.MessageEntity{{Type: "url", Offset: 0, Length: 4}, {Type: "custom_emoji", Offset: 11, Length: 5, CustomEmojiID: "emoji-1"}}})
	}
	s.fake.mu.Lock()
	updates := append([]botapi.Update(nil), s.fake.pending...)
	s.fake.mu.Unlock()
	for i, origin := range origins {
		msg := updates[i].Message
		if msg.ForwardOrigin.Type != origin.Type || len(msg.Entities) != 2 || msg.Entities[1].CustomEmojiID != "emoji-1" {
			t.Fatalf("message %d: %#v", i, msg)
		}
	}
}
