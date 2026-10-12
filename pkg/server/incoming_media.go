package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// IncomingMedia is a user's uploaded file. Data is base64 in the simulation
// API's JSON; the Go API accepts bytes. Type names photo/document/video/audio/
// voice/animation/video_note. Caption is separate from the text message field.
type IncomingMedia struct {
	IncomingMessage
	Type     string `json:"type"`
	Data     []byte `json:"data"`
	FileName string `json:"file_name"`
	Caption  string `json:"caption,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Length   int    `json:"length,omitempty"`
}

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
	if utf8.RuneCountInString(in.Caption) > mediaCaptionMax {
		return 0, 0, fmt.Errorf("caption is too long")
	}
	in.normalize()
	file := &storedFile{kind: in.Type, name: in.FileName, data: append([]byte(nil), in.Data...), width: in.Width, height: in.Height, duration: in.Duration, length: in.Length, mimeType: http.DetectContentType(in.Data)}
	if file.name == "" {
		file.name = in.Type
	}
	if in.Type == "photo" {
		config, _, err := image.DecodeConfig(bytes.NewReader(file.data))
		if err != nil {
			return 0, 0, fmt.Errorf("photo cannot be decoded: %w", err)
		}
		file.width, file.height = config.Width, config.Height
	}
	user := &botapi.User{ID: in.UserID, Username: in.Username, FirstName: in.FirstName}
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.ensureChatLocked(in.ChatID, in.ChatType, in.ChatTitle, user)
	file = s.keepFileLocked(file)
	message := &botapi.Message{From: user, Chat: chat.wire(), Date: s.now().Unix(), Caption: in.Caption}
	attachFile(message, file, nil)
	stored := chat.appendLocked(message, false, "", false)
	stored.file = file
	return s.pushUpdateLocked(botapi.Update{Message: stored.clone()}), stored.msg.MessageID, nil
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
