package server

import "testing"

func TestIncomingForumTopicMessage(t *testing.T) {
	s := newStand(t, Options{})
	code, body := s.sim("POST", "/sim/message", IncomingMessage{ChatID: -100, ChatType: "supergroup", Text: "topic", MessageThreadID: 77, IsTopicMessage: true})
	if code != 200 {
		t.Fatalf("topic: %d %v", code, body)
	}
	s.fake.mu.Lock()
	message := s.fake.pending[0].Message
	s.fake.mu.Unlock()
	if message.MessageThreadID != 77 || !message.IsTopicMessage {
		t.Fatalf("message: %#v", message)
	}
}
