package server

import (
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestIncomingSticker(t *testing.T) {
	s := newStand(t, Options{})
	sticker := &botapi.Sticker{FileID: "sticker-1", FileUniqueID: "usticker-1", Type: "regular", Width: 512, Height: 512, Emoji: "🙂"}
	code, body := s.sim("POST", "/sim/message", IncomingMessage{Sticker: sticker})
	if code != 200 {
		t.Fatalf("sticker: %d %v", code, body)
	}
	s.fake.mu.Lock()
	update := s.fake.pending[0]
	s.fake.mu.Unlock()
	if update.Message == nil || update.Message.Sticker == nil || update.Message.Sticker.FileID != "sticker-1" {
		t.Fatalf("update: %#v", update)
	}
}
