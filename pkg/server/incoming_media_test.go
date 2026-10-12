package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// pngHeader is a PNG of a few dozen bytes that declares a w x h picture: the
// signature, a valid IHDR and IEND, no pixels.
func pngHeader(w, h uint32) []byte {
	chunk := func(kind string, data []byte) []byte {
		var out []byte
		out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
		body := append([]byte(kind), data...)
		out = append(out, body...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(body))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // 8-bit RGB
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	return append(out, chunk("IEND", nil)...)
}

// pollMessage takes the pending updates and returns the message of the last.
func (s *stand) pollMessage() map[string]any {
	s.t.Helper()
	_, body := s.call("getUpdates", nil)
	batch := updates(s.t, body)
	if len(batch) == 0 {
		s.t.Fatal("no update")
	}
	msg, _ := batch[len(batch)-1]["message"].(map[string]any)
	if msg == nil {
		s.t.Fatalf("the update carries no message: %v", batch)
	}
	return msg
}

func TestInjectMediaRefusesWhatAClientCannotSend(t *testing.T) {
	s := newStand(t, Options{})
	cases := map[string]IncomingMedia{
		"unknown type":      {Type: "sticker", Data: []byte("x")},
		"no bytes":          {Type: "document", FileName: "empty.txt"},
		"not a picture":     {Type: "photo", Data: []byte("not a picture")},
		"text, not caption": {IncomingMessage: IncomingMessage{Text: "hello"}, Type: "document", Data: []byte("x")},
		"caption too long":  {Type: "document", Data: []byte("x"), Caption: strings.Repeat("a", mediaCaptionMax+1)},
		"mention too long":  {IncomingMessage: IncomingMessage{Mention: true}, Type: "document", Data: []byte("x"), Caption: strings.Repeat("a", mediaCaptionMax-5)},
	}
	for name, in := range cases {
		if _, _, err := s.fake.InjectMedia(in); err == nil {
			t.Errorf("%s: injected, want an error", name)
		}
	}
	if s.fake.PendingUpdates() != 0 || len(s.fake.Chats()) != 0 {
		t.Errorf("a refused file left %d updates and %d chats", s.fake.PendingUpdates(), len(s.fake.Chats()))
	}
}

// A few dozen bytes can declare a picture whose pixels take gigabytes: a
// photo is refused on its header when its sides add up to more than Telegram
// takes, before anything is decoded.
func TestInjectMediaRefusesAPhotoTooLargeOnItsHeader(t *testing.T) {
	s := newStand(t, Options{})
	if cfg, err := png.DecodeConfig(bytes.NewReader(pngHeader(9000, 9000))); err != nil || cfg.Width != 9000 {
		t.Fatalf("the test header does not read as a PNG: %+v %v", cfg, err)
	}
	_, _, err := s.fake.InjectMedia(IncomingMedia{Type: "photo", Data: pngHeader(9000, 9000)})
	if err == nil || !strings.Contains(err.Error(), "at most 10000") {
		t.Fatalf("a 9000x9000 photo: %v, want a refusal naming the 10000 limit", err)
	}
}

func TestInjectMediaSmallPhotoHasOneSize(t *testing.T) {
	s := newStand(t, Options{})
	pic := mediaPNG(t, 40, 30)
	if _, _, err := s.fake.InjectMedia(IncomingMedia{Type: "photo", Data: pic}); err != nil {
		t.Fatal(err)
	}
	sizes, _ := s.pollMessage()["photo"].([]any)
	if len(sizes) != 1 {
		t.Fatalf("a 40x30 photo has %d sizes, want 1 (no thumbnail above its own size): %v", len(sizes), sizes)
	}
	if got, ok := s.fake.File(sizes[0].(map[string]any)["file_id"].(string)); !ok || !bytes.Equal(got, pic) {
		t.Error("the photo was not kept as sent")
	}
}

func TestInjectMediaThumbnailIsTheSmallerPicture(t *testing.T) {
	s := newStand(t, Options{})
	if _, _, err := s.fake.InjectMedia(IncomingMedia{Type: "photo", Data: mediaPNG(t, 150, 300)}); err != nil {
		t.Fatal(err)
	}
	sizes, _ := s.pollMessage()["photo"].([]any)
	if len(sizes) != 2 {
		t.Fatalf("sizes = %v, want a thumbnail and the photo", sizes)
	}
	thumb := sizes[0].(map[string]any)
	if thumb["width"] != float64(45) || thumb["height"] != float64(90) {
		t.Errorf("thumbnail = %v, want 45x90", thumb)
	}
	data, _ := s.fake.File(thumb["file_id"].(string))
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || cfg.Width != 45 || cfg.Height != 90 {
		t.Errorf("thumbnail bytes are %s %dx%d (%v), want a 45x90 JPEG", format, cfg.Width, cfg.Height, err)
	}
	if int(thumb["file_size"].(float64)) != len(data) {
		t.Errorf("thumbnail file_size = %v, bytes %d", thumb["file_size"], len(data))
	}
}

func TestInjectMediaMentionReplyTypeAndChatView(t *testing.T) {
	s := newStand(t, Options{})
	_, first := s.fake.InjectMessage(IncomingMessage{ChatType: "group", Text: "earlier"})
	_, _, err := s.fake.InjectMedia(IncomingMedia{
		IncomingMessage: IncomingMessage{ChatType: "group", Mention: true, ReplyToMessageID: first},
		Type:            "document", Data: []byte("a,b\n1,2\n"), FileName: "table.csv", MimeType: "text/csv", Caption: "sum it",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := s.pollMessage()
	if msg["caption"] != "@tgfake_bot sum it" {
		t.Errorf("caption = %q, want the bot's mention in front", msg["caption"])
	}
	entities, _ := msg["caption_entities"].([]any)
	if len(entities) != 1 || entities[0].(map[string]any)["type"] != "mention" || entities[0].(map[string]any)["length"] != float64(len("@tgfake_bot")) {
		t.Errorf("caption_entities = %v, want the mention", entities)
	}
	if msg["entities"] != nil || msg["text"] != nil {
		t.Errorf("a media message carries text or entities: %v", msg)
	}
	if reply, _ := msg["reply_to_message"].(map[string]any); reply == nil || reply["text"] != "earlier" {
		t.Errorf("reply_to_message = %v", msg["reply_to_message"])
	}
	if doc := msg["document"].(map[string]any); doc["mime_type"] != "text/csv" {
		t.Errorf("mime_type = %v, want the text/csv the client named", doc["mime_type"])
	}
	chatID := int64(msg["chat"].(map[string]any)["id"].(float64))
	view := s.fake.Chat(chatID)
	last := view.Messages[len(view.Messages)-1]
	if last.From != "user" || last.Document == nil || last.Caption != "@tgfake_bot sum it" || last.ReplyToMessageID != first {
		t.Errorf("chat view = %+v", last)
	}
}

func TestInjectMediaCommandCaptionHasACommandEntity(t *testing.T) {
	s := newStand(t, Options{})
	if _, _, err := s.fake.InjectMedia(IncomingMedia{Type: "document", Data: []byte("x"), FileName: "a.txt", Caption: "/start now"}); err != nil {
		t.Fatal(err)
	}
	entities, _ := s.pollMessage()["caption_entities"].([]any)
	if len(entities) != 1 || entities[0].(map[string]any)["type"] != "bot_command" || entities[0].(map[string]any)["length"] != float64(6) {
		t.Errorf("caption_entities = %v, want a bot_command of 6", entities)
	}
}

func TestSimMediaCarriesTheAlbumAndTheType(t *testing.T) {
	s := newStand(t, Options{})
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<17) // 2 MB, over the 1 MB of the other routes
	status, body := s.sim("POST", "/sim/media", map[string]any{
		"type": "document", "file_name": "big.txt", "mime_type": "text/plain; charset=utf-8", "caption": "here", "media_group_id": "g1",
		"data": base64.StdEncoding.EncodeToString(big),
	})
	if status != http.StatusOK || body["message_id"] == nil {
		t.Fatalf("/sim/media = %d %v", status, body)
	}
	msg := s.pollMessage()
	if msg["caption"] != "here" || msg["media_group_id"] != "g1" {
		t.Errorf("message = %v", msg)
	}
	doc := msg["document"].(map[string]any)
	if doc["mime_type"] != "text/plain" {
		t.Errorf("mime_type = %v, want text/plain without its parameters", doc["mime_type"])
	}
	if got, _ := s.fake.File(doc["file_id"].(string)); !bytes.Equal(got, big) {
		t.Error("/sim/media did not keep the decoded bytes")
	}
	if view := s.fake.Chat(4242); view.Messages[0].MediaGroupID != "g1" {
		t.Errorf("chat view media group = %q", view.Messages[0].MediaGroupID)
	}
}
