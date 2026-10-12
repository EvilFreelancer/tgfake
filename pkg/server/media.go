package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func filePath(f *storedFile) string { return f.kind + "/" + f.id }

func (s *Server) getFile(w http.ResponseWriter, method string, params url.Values) {
	id := params.Get("file_id")
	if id == "" {
		s.writeError(w, method, params, 400, "Bad Request: file_id not specified", 0)
		return
	}
	s.mu.Lock()
	f := s.files[id]
	var result botapi.File
	problem := ""
	if f == nil {
		problem = "Bad Request: wrong file_id or the file is temporarily unavailable"
	} else if len(f.data) > 20<<20 {
		problem = "Bad Request: file is too big"
	} else {
		result = botapi.File{FileID: f.id, FileUniqueID: "u" + f.id, FileSize: len(f.data), FilePath: filePath(f)}
	}
	s.mu.Unlock()
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.writeResult(w, method, params, result)
}

func (s *Server) serveFile(w http.ResponseWriter, req *http.Request) {
	rest, ok := strings.CutPrefix(req.URL.Path, "/file/bot")
	token, relative, found := strings.Cut(rest, "/")
	if !ok || !found {
		s.writeError(w, "", nil, 404, "Not Found", 0)
		return
	}
	if s.opts.Token != "" && token != s.opts.Token {
		s.writeError(w, "", nil, 404, "Not Found", 0)
		return
	}
	s.mu.Lock()
	var f *storedFile
	for _, candidate := range s.files {
		if filePath(candidate) == relative {
			f = candidate
			break
		}
	}
	s.mu.Unlock()
	if f == nil {
		s.writeError(w, "", nil, 404, "Not Found", 0)
		return
	}
	w.Header().Set("Content-Type", f.mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(f.data)))
	_, _ = w.Write(f.data)
}

// resolveMedia reads an upload, attach reference, known file id or HTTP URL.
// Resolution does not allocate file/message ids, so an album can fail atomically.
func (s *Server) resolveMedia(req *http.Request, params url.Values, kind, field string) (*storedFile, string) {
	reference := strings.TrimSpace(params.Get(field))
	partName := field
	if strings.HasPrefix(reference, "attach://") {
		partName = strings.TrimPrefix(reference, "attach://")
	}
	limit := int64(documentMaxSize)
	if kind == "photo" {
		limit = photoMaxSize
	}
	var data []byte
	name := ""
	if req.MultipartForm != nil && len(req.MultipartForm.File[partName]) > 0 {
		header := req.MultipartForm.File[partName][0]
		file, err := header.Open()
		if err != nil {
			return nil, "Bad Request: there is no " + strings.ReplaceAll(kind, "_", " ") + " in the request"
		}
		data, err = io.ReadAll(io.LimitReader(file, limit+1))
		_ = file.Close()
		if err != nil {
			return nil, "Bad Request: can't read the " + kind
		}
		name = header.Filename
	} else if strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://") {
		if kind == "video_note" {
			return nil, "Bad Request: there is no video note in the request"
		}
		u, err := url.Parse(reference)
		if err != nil || u.Host == "" {
			return nil, "Bad Request: Wrong HTTP URL specified"
		}
		fetchCtx, cancel := context.WithCancel(req.Context())
		defer cancel()
		stop := context.AfterFunc(s.networkCtx, cancel)
		defer stop()
		fetch, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, reference, nil)
		if err != nil {
			return nil, "Bad Request: Wrong HTTP URL specified"
		}
		response, err := s.opts.HTTPClient.Do(fetch)
		if err != nil {
			return nil, "Bad Request: Failed to get HTTP URL content"
		}
		if kind == "photo" {
			limit = 5 << 20
		} else {
			limit = 20 << 20
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
		_ = response.Body.Close()
		if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, "Bad Request: Failed to get HTTP URL content"
		}
		name = path.Base(u.Path)
		if name == "." || name == "/" {
			name = kind
		}
	} else if reference != "" && !strings.HasPrefix(reference, "attach://") {
		s.mu.Lock()
		f := s.files[reference]
		s.mu.Unlock()
		if f != nil && f.kind == kind {
			return f, ""
		}
		return nil, "Bad Request: wrong file_id or the file is temporarily unavailable"
	} else {
		return nil, "Bad Request: there is no " + strings.ReplaceAll(kind, "_", " ") + " in the request"
	}
	if int64(len(data)) > limit {
		return nil, "Bad Request: file is too big"
	}
	f := &storedFile{name: name, data: data, kind: kind, width: atoi(params.Get("width")), height: atoi(params.Get("height")), duration: atoi(params.Get("duration")), length: atoi(params.Get("length")), performer: params.Get("performer"), title: params.Get("title")}
	f.mimeType, _, _ = strings.Cut(http.DetectContentType(data), ";")
	if kind == "photo" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, "Bad Request: IMAGE_PROCESS_FAILED"
		}
		f.width, f.height = cfg.Width, cfg.Height
	}
	return f, ""
}

func (s *Server) keepFileLocked(f *storedFile) *storedFile {
	if f.id != "" {
		return f
	}
	copy := *f
	s.nextFileID++
	copy.id = "file_" + strconv.Itoa(s.nextFileID)
	s.files[copy.id] = &copy
	return &copy
}

func attachFile(msg *botapi.Message, f *storedFile, params url.Values) {
	msg.Photo, msg.Document, msg.Video, msg.Animation, msg.Audio, msg.Voice, msg.VideoNote = nil, nil, nil, nil, nil, nil, nil
	id, unique, size := f.id, "u"+f.id, len(f.data)
	switch f.kind {
	case "photo":
		msg.Photo = []botapi.PhotoSize{{FileID: id, FileUniqueID: unique, FileSize: size, Width: f.width, Height: f.height}}
	case "document":
		msg.Document = &botapi.Document{FileID: id, FileUniqueID: unique, FileSize: size, FileName: f.name, MimeType: f.mimeType}
	case "video", "animation":
		v := &botapi.Video{FileID: id, FileUniqueID: unique, FileSize: size, FileName: f.name, MimeType: f.mimeType, Width: f.width, Height: f.height, Duration: f.duration}
		if f.kind == "video" {
			msg.Video = v
		} else {
			msg.Animation = v
		}
	case "audio", "voice":
		a := &botapi.Audio{FileID: id, FileUniqueID: unique, FileSize: size, MimeType: f.mimeType, Duration: f.duration}
		if f.kind == "audio" {
			a.FileName, a.Performer, a.Title = f.name, f.performer, f.title
			msg.Audio = a
		} else {
			msg.Voice = a
		}
	case "video_note":
		msg.VideoNote = &botapi.VideoNote{FileID: id, FileUniqueID: unique, FileSize: size, Length: f.length, Duration: f.duration}
	}
}

func (s *Server) sendMedia(w http.ResponseWriter, req *http.Request, method string, params url.Values, kind string) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	f, problem := s.resolveMedia(req, params, kind, kind)
	if problem == "" && utf8.RuneCountInString(params.Get("caption")) > mediaCaptionMax {
		problem = "Bad Request: message caption is too long"
	}
	markup, keyboardProblem := s.keyboardFor(chatID, params)
	if problem == "" {
		problem = keyboardProblem
	}
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	quoted, problem := replyTargetLocked(chat, params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	if f.id == "" {
		params.Set(kind, fmt.Sprintf("%s (%d bytes)", f.name, len(f.data)))
	}
	f = s.keepFileLocked(f)
	msg := &botapi.Message{From: s.botUser(), Chat: chat.wire(), Date: s.now().Unix(), Caption: params.Get("caption"), ReplyToMessage: quoted, ReplyMarkup: markup, HasProtectedContent: params.Get("protect_content") == "true"}
	if kind == "video_note" {
		msg.Caption = ""
	}
	attachFile(msg, f, params)
	stored := chat.appendLocked(msg, true, params.Get("parse_mode"), false)
	stored.file = f
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

type inputMedia struct {
	Type      string `json:"type"`
	Media     string `json:"media"`
	Caption   string `json:"caption"`
	ParseMode string `json:"parse_mode"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Duration  int    `json:"duration"`
	Performer string `json:"performer"`
	Title     string `json:"title"`
}

func (in inputMedia) params() url.Values {
	return url.Values{"media": {in.Media}, "caption": {in.Caption}, "parse_mode": {in.ParseMode}, "width": {strconv.Itoa(in.Width)}, "height": {strconv.Itoa(in.Height)}, "duration": {strconv.Itoa(in.Duration)}, "performer": {in.Performer}, "title": {in.Title}}
}

func parseInputMedia(raw json.RawMessage) (inputMedia, string) {
	var item inputMedia
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return item, "Bad Request: can't parse InputMedia: expected an Object"
	}
	if _, ok := fields["type"]; !ok {
		return item, "Bad Request: can't parse InputMedia: Can't find field \"type\""
	}
	if json.Unmarshal(raw, &item) != nil {
		return item, "Bad Request: can't parse InputMedia: Field \"type\" must be of type String"
	}
	if item.Media == "" {
		return item, "Bad Request: can't parse InputMedia: media not found"
	}
	return item, ""
}

func (s *Server) sendMediaGroup(w http.ResponseWriter, req *http.Request, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	var rawItems []json.RawMessage
	problem := ""
	raw := params.Get("media")
	if raw == "" {
		problem = "Bad Request: parameter \"media\" is required"
	} else if !json.Valid([]byte(raw)) {
		problem = "Bad Request: can't parse media JSON object"
	} else if json.Unmarshal([]byte(raw), &rawItems) != nil {
		problem = "Bad Request: expected an Array of InputMedia"
	}
	items := make([]inputMedia, len(rawItems))
	for i, raw := range rawItems {
		if problem != "" {
			break
		}
		items[i], problem = parseInputMedia(raw)
	}
	if problem == "" && len(items) == 0 {
		problem = "Bad Request: there are no messages to send"
	}
	if problem == "" && len(items) > 10 {
		problem = "Bad Request: too many messages to send as an album"
	}
	files := make([]*storedFile, len(items))
	for i, item := range items {
		if problem != "" {
			break
		}
		if item.Type != "photo" && item.Type != "video" && item.Type != "audio" && item.Type != "document" {
			problem = "Bad Request: can't parse InputMedia: type \"" + item.Type + "\" can't be used in sendMediaGroup"
			break
		}
		if (items[0].Type == "audio" || items[0].Type == "document" || item.Type == "audio" || item.Type == "document") && items[0].Type != item.Type {
			kind := item.Type
			if items[0].Type == "audio" || items[0].Type == "document" {
				kind = items[0].Type
			}
			problem = "Bad Request: " + kind + " can't be mixed with other media types"
			break
		}
		if utf8.RuneCountInString(item.Caption) > mediaCaptionMax {
			problem = "Bad Request: message caption is too long"
			break
		}
		files[i], problem = s.resolveMedia(req, item.params(), item.Type, "media")
	}
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	quoted, problem := replyTargetLocked(chat, params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.nextAlbum++
	groupID := strconv.Itoa(s.nextAlbum)
	result := make([]*botapi.Message, 0, len(items))
	for i, item := range items {
		f := s.keepFileLocked(files[i])
		msg := &botapi.Message{From: s.botUser(), Chat: chat.wire(), Date: s.now().Unix(), Caption: item.Caption, ReplyToMessage: quoted, MediaGroupID: groupID, HasProtectedContent: params.Get("protect_content") == "true"}
		attachFile(msg, f, item.params())
		stored := chat.appendLocked(msg, true, item.ParseMode, false)
		stored.file = f
		result = append(result, stored.clone())
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

func (s *Server) editMedia(w http.ResponseWriter, req *http.Request, method string, params url.Values) {
	var replacement *storedFile
	var media inputMedia
	problem := ""
	if strings.EqualFold(method, "editMessageMedia") {
		raw := params.Get("media")
		if raw == "" {
			problem = "Bad Request: parameter \"media\" is required"
		} else if !json.Valid([]byte(raw)) {
			problem = "Bad Request: can't parse input media JSON object"
		} else {
			media, problem = parseInputMedia(json.RawMessage(raw))
		}
		if problem == "" {
			switch media.Type {
			case "photo", "video", "animation", "audio", "document":
				replacement, problem = s.resolveMedia(req, media.params(), media.Type, "media")
			default:
				problem = "Bad Request: can't parse InputMedia: type \"" + media.Type + "\" is unsupported"
			}
		}
	}
	caption := params.Get("caption")
	if replacement != nil {
		caption = media.Caption
	}
	if problem == "" && utf8.RuneCountInString(caption) > mediaCaptionMax {
		problem = "Bad Request: message caption is too long"
	}
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.mu.Lock()
	target, inline, problem := s.editTargetLocked(params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	markup, problem := parseKeyboard(params.Get("reply_markup"))
	if problem == "" {
		problem = validateKeyboard(markup, target.msg.Chat.Type)
	}
	if target.file == nil && replacement == nil && problem == "" {
		problem = "Bad Request: there is no caption in the message to edit"
	}
	if problem == "" && replacement != nil && target.msg.MediaGroupID != "" {
		oldKind := target.file.kind
		if (oldKind == "audio" || oldKind == "document" || replacement.kind == "audio" || replacement.kind == "document") && replacement.kind != oldKind {
			problem = "Bad Request: Can't use the media of the specified type in the album"
		}
	}
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	before := target.clone()
	newMessage := *before
	newMessage.Caption, newMessage.ReplyMarkup = caption, markup
	if replacement != nil {
		attachFile(&newMessage, replacement, media.params())
		newMessage.Text = ""
	}
	if reflect.DeepEqual(before, &newMessage) {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, notModified, 0)
		return
	}
	if replacement != nil {
		replacement = s.keepFileLocked(replacement)
		attachFile(&newMessage, replacement, media.params())
		target.file = replacement
		target.rich = false
		target.parseMode = media.ParseMode
	} else {
		target.parseMode = params.Get("parse_mode")
	}
	newMessage.EditDate = s.now().Unix()
	target.msg = newMessage
	target.edited = true
	result := target.clone()
	s.mu.Unlock()
	if inline {
		s.writeResult(w, method, params, true)
	} else {
		s.writeResult(w, method, params, result)
	}
}

const notModified = "Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message"

func (s *Server) editTargetLocked(params url.Values) (*storedMessage, bool, string) {
	if id := params.Get("inline_message_id"); id != "" {
		target := s.inlineMessages[id]
		if target == nil || target.deleted {
			return nil, true, "Bad Request: message to edit not found"
		}
		return target, true, ""
	}
	chatID, ok := chatIDOf(params)
	if !ok {
		return nil, false, "Bad Request: chat_id is empty"
	}
	chat := s.chats[chatID]
	if chat == nil || chat.byID[atoi(params.Get("message_id"))] == nil || chat.byID[atoi(params.Get("message_id"))].deleted {
		return nil, false, "Bad Request: message to edit not found"
	}
	target := chat.byID[atoi(params.Get("message_id"))]
	if !target.fromBot && target.msg.ViaBot == nil {
		return nil, false, "Bad Request: message can't be edited"
	}
	return target, false, ""
}
