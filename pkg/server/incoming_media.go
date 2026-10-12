package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// IncomingMedia is a user's uploaded file. Data is base64 in the simulation
// API's JSON; the Go API accepts bytes. Type names photo/document/video/audio/
// voice/animation/video_note. Caption is separate from the text message field:
// a media message carries no text. The chat, the person, ReplyToMessageID and
// Mention of the embedded IncomingMessage apply as for a text message, Mention
// putting the bot's @username in front of the caption.
type IncomingMedia struct {
	IncomingMessage
	Type     string `json:"type"`
	Data     []byte `json:"data"`
	FileName string `json:"file_name"`
	// MimeType is the type the person's client names; empty detects it from
	// the bytes.
	MimeType string `json:"mime_type,omitempty"`
	Caption  string `json:"caption,omitempty"`
	// MediaGroupID marks the messages of one album: Telegram delivers an album
	// as one message per item sharing a media_group_id, with the caption on
	// one of them.
	MediaGroupID string `json:"media_group_id,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	Length       int    `json:"length,omitempty"`
}

// thumbnailEdge is the long side of the smallest size Telegram gives an
// incoming photo, its "s" thumbnail.
const thumbnailEdge = 90

// InjectMedia stores the user's bytes and queues a message carrying its file id.
func (s *Server) InjectMedia(in IncomingMedia) (int, int, error) {
	switch in.Type {
	case "photo", "document", "video", "audio", "voice", "animation", "video_note":
	default:
		return 0, 0, fmt.Errorf("unsupported media type %q", in.Type)
	}
	limit := documentMaxSize
	if in.Type == "photo" {
		limit = photoMaxSize
	}
	if len(in.Data) == 0 || len(in.Data) > limit {
		return 0, 0, fmt.Errorf("file must contain 1-%d bytes", limit)
	}
	if in.Text != "" {
		return 0, 0, fmt.Errorf("a media message carries a caption, not a text")
	}
	in.normalize()
	caption := in.Caption
	var entities []botapi.MessageEntity
	if in.Mention {
		mention := "@" + s.opts.BotUsername
		caption = strings.TrimRight(mention+" "+caption, " ")
		entities = append(entities, botapi.MessageEntity{Type: "mention", Offset: 0, Length: utf16Len(mention)})
	}
	if m := commandPattern.FindString(caption); m != "" {
		entities = append(entities, botapi.MessageEntity{Type: "bot_command", Offset: 0, Length: utf16Len(m)})
	}
	if utf8.RuneCountInString(caption) > mediaCaptionMax {
		return 0, 0, fmt.Errorf("caption is too long")
	}
	mimeType, _, _ := strings.Cut(in.MimeType, ";")
	if mimeType == "" {
		mimeType, _, _ = strings.Cut(http.DetectContentType(in.Data), ";")
	}
	file := &storedFile{kind: in.Type, name: in.FileName, data: append([]byte(nil), in.Data...), width: in.Width, height: in.Height, duration: in.Duration, length: in.Length, mimeType: mimeType}
	if file.name == "" {
		file.name = in.Type
	}
	var thumb *storedFile
	if in.Type == "photo" {
		img, _, err := image.Decode(bytes.NewReader(file.data))
		if err != nil {
			return 0, 0, fmt.Errorf("photo cannot be decoded: %w", err)
		}
		file.width, file.height = img.Bounds().Dx(), img.Bounds().Dy()
		if thumb, err = photoThumbnail(img, file.name); err != nil {
			return 0, 0, err
		}
	}
	user := &botapi.User{ID: in.UserID, Username: in.Username, FirstName: in.FirstName}
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.ensureChatLocked(in.ChatID, in.ChatType, in.ChatTitle, user)
	file = s.keepFileLocked(file)
	message := &botapi.Message{From: user, Chat: chat.wire(), Date: s.now().Unix(), Caption: caption, CaptionEntities: entities, MediaGroupID: in.MediaGroupID}
	if in.ReplyToMessageID != 0 {
		if target := chat.byID[in.ReplyToMessageID]; target != nil {
			message.ReplyToMessage = target.quoted()
		}
	}
	attachFile(message, file, nil)
	if thumb != nil {
		// Telegram lists a photo's sizes from the smallest up, so a bot that
		// takes the last one gets the full picture.
		thumb = s.keepFileLocked(thumb)
		size := botapi.PhotoSize{FileID: thumb.id, FileUniqueID: "u" + thumb.id, FileSize: len(thumb.data), Width: thumb.width, Height: thumb.height}
		message.Photo = append([]botapi.PhotoSize{size}, message.Photo...)
	}
	stored := chat.appendLocked(message, false, "", false)
	stored.file = file
	return s.pushUpdateLocked(botapi.Update{Message: stored.clone()}), stored.msg.MessageID, nil
}

// photoThumbnail is the "s" size of an incoming photo larger than a
// thumbnail: the picture sampled down to thumbnailEdge pixels on its long side,
// as a JPEG. A picture that small already gets no thumbnail.
func photoThumbnail(img image.Image, name string) (*storedFile, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) <= thumbnailEdge {
		return nil, nil
	}
	nw, nh := thumbnailEdge, max(1, h*thumbnailEdge/w)
	if h > w {
		nw, nh = max(1, w*thumbnailEdge/h), thumbnailEdge
	}
	small := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			small.Set(x, y, img.At(b.Min.X+x*w/nw, b.Min.Y+y*h/nh))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("photo thumbnail cannot be made: %w", err)
	}
	return &storedFile{kind: "photo", name: name, mimeType: "image/jpeg", data: buf.Bytes(), width: nw, height: nh}, nil
}

func (s *Server) simMedia(w http.ResponseWriter, req *http.Request) {
	var in IncomingMedia
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 72<<20))
	if err := decoder.Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}
	update, message, err := s.InjectMedia(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": update, "message_id": message})
}
