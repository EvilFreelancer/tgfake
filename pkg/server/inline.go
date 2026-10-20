package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

type webQuery struct {
	chatID int64
	user   botapi.User
}
type inlineQueryState struct {
	query    botapi.InlineQuery
	results  []json.RawMessage
	answered bool
}
type preparedMessage struct {
	result                                json.RawMessage
	userID                                int64
	expires                               int64
	userChats, botChats, groups, channels bool
}

// IncomingInlineQuery is an inline query; zero user fields name the default user.
type IncomingInlineQuery struct {
	UserID    int64            `json:"user_id,omitempty"`
	Username  string           `json:"username,omitempty"`
	FirstName string           `json:"first_name,omitempty"`
	Query     string           `json:"query"`
	Offset    string           `json:"offset,omitempty"`
	ChatType  string           `json:"chat_type,omitempty"`
	Location  *botapi.Location `json:"location,omitempty"`
}

// InlineChoice selects a previously answered query result into a chat.
type InlineChoice struct {
	QueryID  string `json:"inline_query_id"`
	ResultID string `json:"result_id"`
	ChatID   int64  `json:"chat_id,omitempty"`
	ChatType string `json:"chat_type,omitempty"`
}

// InlineQueryView is a query with the results the bot offered.
type InlineQueryView struct {
	Query    botapi.InlineQuery `json:"query"`
	Answered bool               `json:"answered"`
	Results  []json.RawMessage  `json:"results"`
}

// InjectInlineQuery queues an inline_query update and returns its update/query ids.
func (s *Server) InjectInlineQuery(in IncomingInlineQuery) (int, string, error) {
	if !s.opts.InlineMode {
		return 0, "", fmt.Errorf("inline mode is disabled; enable InlineMode or --inline")
	}
	user := IncomingMessage{UserID: in.UserID, Username: in.Username, FirstName: in.FirstName}
	user.normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextInline++
	id := strconv.Itoa(s.nextInline)
	query := botapi.InlineQuery{ID: id, From: botapi.User{ID: user.UserID, FirstName: user.FirstName, Username: user.Username}, Query: in.Query, Offset: in.Offset, ChatType: in.ChatType, Location: copyPointer(in.Location)}
	s.inlineQueries[id] = &inlineQueryState{query: query}
	return s.pushUpdateLocked(botapi.Update{InlineQuery: &query}), id, nil
}

// InlineQueries returns copied queries and answers, oldest first.
func (s *Server) InlineQueries() []InlineQueryView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := []InlineQueryView{}
	for id := 1; id <= s.nextInline; id++ {
		state := s.inlineQueries[strconv.Itoa(id)]
		if state == nil {
			continue
		}
		view := InlineQueryView{Query: state.query, Answered: state.answered, Results: []json.RawMessage{}}
		view.Query.Location = copyPointer(view.Query.Location)
		for _, result := range state.results {
			view.Results = append(view.Results, append(json.RawMessage(nil), result...))
		}
		views = append(views, view)
	}
	return views
}

type inlineResult struct {
	Type                string                     `json:"type"`
	ID                  string                     `json:"id"`
	Title               string                     `json:"title"`
	Caption             string                     `json:"caption"`
	ParseMode           string                     `json:"parse_mode"`
	ReplyMarkup         json.RawMessage            `json:"reply_markup"`
	InputMessageContent map[string]json.RawMessage `json:"input_message_content"`
	raw                 map[string]json.RawMessage
}

func parseInlineResult(raw json.RawMessage) (inlineResult, string) {
	var result inlineResult
	if len(raw) == 0 {
		return result, "Bad Request: result isn't specified"
	}
	if json.Unmarshal(raw, &result) != nil || json.Unmarshal(raw, &result.raw) != nil || result.raw == nil {
		return result, "Bad Request: inline query result must be an object"
	}
	if len(result.ID) == 0 || len(result.ID) > 64 {
		return result, "Bad Request: RESULT_ID_INVALID"
	}
	switch result.Type {
	case "article", "photo", "video", "audio", "voice", "document", "gif", "mpeg4_gif", "location", "venue", "contact":
	default:
		return result, "Bad Request: type \"" + result.Type + "\" is unsupported for the inline query result"
	}
	markup, problem := parseKeyboard(string(result.ReplyMarkup))
	if problem != "" {
		return result, problem
	}
	if problem := validateKeyboard(markup, "private", false); problem != "" {
		return result, problem
	}
	params := result.params()
	required := []string{}
	switch result.Type {
	case "article", "location", "video", "voice", "document":
		required = append(required, "title")
	case "audio":
		if params.Get("audio_url") != "" {
			required = append(required, "title")
		}
	}
	switch result.Type {
	case "photo", "video", "audio", "voice", "document", "gif", "mpeg4_gif":
		prefix := result.Type
		if prefix == "mpeg4_gif" {
			prefix = "mpeg4"
		}
		if params.Get(prefix+"_url") == "" {
			required = append(required, prefix+"_file_id")
		}
		if (result.Type == "video" || result.Type == "document") && params.Get(prefix+"_url") != "" {
			required = append(required, "mime_type")
		}
	}
	for _, field := range required {
		if _, ok := result.raw[field]; !ok {
			return result, "Bad Request: can't find field \"" + field + "\""
		}
	}
	if result.Type == "location" || result.Type == "venue" || result.Type == "contact" {
		if _, problem := contentMessage(params, "send"+result.Type); problem != "" {
			return result, problem
		}
	}
	if result.Type == "article" && len(result.InputMessageContent) == 0 {
		return result, "Bad Request: message text is empty"
	}
	if content := result.InputMessageContent; content != nil {
		if raw, ok := content["message_text"]; ok {
			var text string
			if json.Unmarshal(raw, &text) != nil || text == "" {
				return result, "Bad Request: message text is empty"
			}
			if utf8.RuneCountInString(text) > messageTextMax {
				return result, "Bad Request: message is too long"
			}
		} else {
			kind := "sendlocation"
			if _, ok := content["phone_number"]; ok {
				kind = "sendcontact"
			} else if _, ok := content["title"]; ok {
				kind = "sendvenue"
			}
			if _, latitude := content["latitude"]; !latitude && kind != "sendcontact" {
				return result, "Bad Request: invalid inline message content specified"
			}
			if _, problem := contentMessage(inlineValues(content), kind); problem != "" {
				return result, problem
			}
		}
	}
	if utf8.RuneCountInString(result.Caption) > mediaCaptionMax {
		return result, "Bad Request: message caption is too long"
	}
	return result, ""
}

func (in inlineResult) params() url.Values {
	return inlineValues(in.raw)
}

func inlineValues(fields map[string]json.RawMessage) url.Values {
	params := url.Values{}
	for key, raw := range fields {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			params.Set(key, value)
		} else {
			params.Set(key, string(raw))
		}
	}
	return params
}

func (s *Server) inlineContent(req *http.Request, result inlineResult) (*botapi.Message, *storedFile, string) {
	params := result.params()
	markup, problem := parseKeyboard(string(result.ReplyMarkup))
	if problem != "" {
		return nil, nil, problem
	}
	message := &botapi.Message{Caption: result.Caption, ReplyMarkup: markup}
	if result.InputMessageContent != nil {
		content := inlineValues(result.InputMessageContent)
		if text := content.Get("message_text"); text != "" {
			message.Text = text
			message.Caption = ""
			return message, nil, ""
		}
		kind := "sendlocation"
		if content.Get("phone_number") != "" {
			kind = "sendcontact"
		} else if content.Get("title") != "" {
			kind = "sendvenue"
		}
		payload, problem := contentMessage(content, kind)
		if problem != "" {
			return nil, nil, problem
		}
		payload.ReplyMarkup = markup
		return payload, nil, ""
	}
	if result.Type == "location" || result.Type == "venue" || result.Type == "contact" {
		payload, problem := contentMessage(params, "send"+result.Type)
		if payload != nil {
			payload.ReplyMarkup = markup
		}
		return payload, nil, problem
	}
	kind := result.Type
	if kind == "gif" || kind == "mpeg4_gif" {
		kind = "animation"
	}
	prefix := result.Type
	if prefix == "mpeg4_gif" {
		prefix = "mpeg4"
	}
	reference := params.Get(prefix + "_file_id")
	if reference == "" {
		reference = params.Get(prefix + "_url")
	}
	for _, field := range []string{"width", "height", "duration"} {
		params.Set(field, params.Get(prefix+"_"+field))
	}
	params.Set("media", reference)
	file, problem := s.resolveMedia(req, params, kind, "media")
	if problem != "" {
		return nil, nil, problem
	}
	return message, file, ""
}

const invalidQuery = "Bad Request: query is too old and response timeout expired or query ID is invalid"

func (s *Server) answerInline(w http.ResponseWriter, req *http.Request, method string, params url.Values) {
	if strings.EqualFold(method, "answerInlineQuery") {
		var results []json.RawMessage
		raw := params.Get("results")
		if raw == "" {
			raw = "[]"
		}
		if json.Unmarshal([]byte(raw), &results) != nil {
			s.writeError(w, method, params, 400, "Bad Request: expected an Array of InlineQueryResult", 0)
			return
		}
		if results == nil {
			results = []json.RawMessage{}
		}
		if len(results) > 50 {
			s.writeError(w, method, params, 400, "Bad Request: too many inline query results specified", 0)
			return
		}
		seen := map[string]bool{}
		for _, raw := range results {
			result, problem := parseInlineResult(raw)
			if problem == "" && seen[result.ID] {
				problem = "Bad Request: RESULT_ID_DUPLICATE"
			}
			if problem != "" {
				s.writeError(w, method, params, 400, problem, 0)
				return
			}
			seen[result.ID] = true
		}
		s.mu.Lock()
		state := s.inlineQueries[params.Get("inline_query_id")]
		if state == nil || state.answered {
			s.mu.Unlock()
			s.writeError(w, method, params, 400, invalidQuery, 0)
			return
		}
		state.answered = true
		state.results = results
		s.mu.Unlock()
		s.writeResult(w, method, params, true)
		return
	}
	result, problem := parseInlineResult(json.RawMessage(params.Get("result")))
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	if strings.EqualFold(method, "savePreparedInlineMessage") {
		userID := integerOf(params.Get("user_id"))
		if userID <= 0 {
			s.writeError(w, method, params, 400, "Bad Request: invalid user_id specified", 0)
			return
		}
		s.mu.Lock()
		s.nextInline++
		id := "prepared-" + strconv.Itoa(s.nextInline)
		expires := s.now().Add(5 * time.Minute).Unix()
		s.prepared[id] = preparedMessage{result: json.RawMessage(params.Get("result")), userID: userID, expires: expires, userChats: params.Get("allow_user_chats") == "true", botChats: params.Get("allow_bot_chats") == "true", groups: params.Get("allow_group_chats") == "true", channels: params.Get("allow_channel_chats") == "true"}
		s.mu.Unlock()
		s.writeResult(w, method, params, botapi.PreparedInlineMessage{ID: id, ExpirationDate: expires})
		return
	}
	id := params.Get("web_app_query_id")
	s.mu.Lock()
	query, ok := s.webQueries[id]
	s.mu.Unlock()
	if !ok {
		s.writeError(w, method, params, 400, invalidQuery, 0)
		return
	}
	message, file, problem := s.inlineContent(req, result)
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.mu.Lock()
	if current, ok := s.webQueries[id]; !ok || current != query {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, invalidQuery, 0)
		return
	}
	delete(s.webQueries, id)
	stored, inlineID := s.storeInlineLocked(query.chatID, "", query.user, message, file, result.ParseMode)
	_ = stored
	s.mu.Unlock()
	s.writeResult(w, method, params, botapi.SentWebAppMessage{InlineMessageID: inlineID})
}

func (s *Server) storeInlineLocked(chatID int64, chatType string, user botapi.User, message *botapi.Message, file *storedFile, parseMode string) (*storedMessage, string) {
	if chatID == 0 {
		chatID = user.ID
	}
	chat := s.ensureChatLocked(chatID, chatType, "", &user)
	message.From, message.Chat, message.Date, message.ViaBot = &user, chat.wire(), s.now().Unix(), s.botUser()
	if file != nil {
		file = s.keepFileLocked(file)
		attachFile(message, file, nil)
	}
	stored := chat.appendLocked(message, false, parseMode, false)
	stored.file = file
	inlineID := ""
	if message.ReplyMarkup != nil {
		s.nextInline++
		inlineID = "inline-" + strconv.Itoa(s.nextInline)
		s.inlineMessages[inlineID] = stored
	}
	return stored, inlineID
}

// ChooseInlineResult sends one of the bot's offered results on behalf of its user
// and queues chosen_inline_result. It returns the message and optional inline id.
func (s *Server) ChooseInlineResult(in InlineChoice) (int, string, error) {
	s.mu.Lock()
	state := s.inlineQueries[in.QueryID]
	var raw json.RawMessage
	if state != nil && state.answered {
		for _, candidate := range state.results {
			var result inlineResult
			_ = json.Unmarshal(candidate, &result)
			if result.ID == in.ResultID {
				raw = append(json.RawMessage(nil), candidate...)
				break
			}
		}
	}
	s.mu.Unlock()
	if raw == nil {
		return 0, "", fmt.Errorf("answered inline query result not found")
	}
	result, problem := parseInlineResult(raw)
	if problem != "" {
		return 0, "", fmt.Errorf("%s", problem)
	}
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	message, file, problem := s.inlineContent(req, result)
	if problem != "" {
		return 0, "", fmt.Errorf("%s", problem)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inlineQueries[in.QueryID] != state {
		return 0, "", fmt.Errorf("inline query was reset")
	}
	stored, id := s.storeInlineLocked(in.ChatID, in.ChatType, state.query.From, message, file, result.ParseMode)
	s.pushUpdateLocked(botapi.Update{ChosenInlineResult: &botapi.ChosenInlineResult{ResultID: in.ResultID, From: state.query.From, Query: state.query.Query, InlineMessageID: id}})
	return stored.msg.MessageID, id, nil
}

// WebAppSubmission is a keyboard Mini App's sendData service message.
type WebAppSubmission struct {
	IncomingMessage
	Data       string `json:"data"`
	ButtonText string `json:"button_text"`
}

// InjectWebAppData delivers a service message without treating its data as text.
func (s *Server) InjectWebAppData(in WebAppSubmission) (int, int, error) {
	if in.Data == "" || len(in.Data) > 4096 {
		return 0, 0, fmt.Errorf("data must contain 1-4096 bytes")
	}
	in.normalize()
	user := &botapi.User{ID: in.UserID, Username: in.Username, FirstName: in.FirstName}
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.ensureChatLocked(in.ChatID, in.ChatType, in.ChatTitle, user)
	message := &botapi.Message{From: user, Chat: chat.wire(), Date: s.now().Unix(), WebAppData: &botapi.WebAppData{Data: in.Data, ButtonText: in.ButtonText}}
	stored := chat.appendLocked(message, false, "", false)
	return s.pushUpdateLocked(botapi.Update{Message: stored.clone()}), stored.msg.MessageID, nil
}

// PreparedMessageShare simulates sharing a stored Mini App message.
type PreparedMessageShare struct {
	ID       string `json:"id"`
	UserID   int64  `json:"user_id,omitempty"`
	ChatID   int64  `json:"chat_id,omitempty"`
	ChatType string `json:"chat_type,omitempty"`
	BotChat  bool   `json:"bot_chat,omitempty"`
}

// SharePreparedMessage checks the allowed user, destination and expiration before
// adding the shared message to the transcript. Sharing can be repeated.
func (s *Server) SharePreparedMessage(in PreparedMessageShare) (int, error) {
	if in.UserID == 0 {
		in.UserID = defaultUserID
	}
	if in.ChatType == "" {
		in.ChatType = "private"
	}
	s.mu.Lock()
	prepared, ok := s.prepared[in.ID]
	now := s.now().Unix()
	s.mu.Unlock()
	if !ok || now >= prepared.expires || in.UserID != prepared.userID {
		return 0, fmt.Errorf("prepared message is unavailable for this user")
	}
	allowed := false
	switch in.ChatType {
	case "private":
		if in.BotChat {
			allowed = prepared.botChats
		} else {
			allowed = prepared.userChats
		}
	case "group", "supergroup":
		allowed = prepared.groups
	case "channel":
		allowed = prepared.channels
	}
	if !allowed {
		return 0, fmt.Errorf("prepared message cannot be shared to this chat type")
	}
	result, problem := parseInlineResult(prepared.result)
	if problem != "" {
		return 0, fmt.Errorf("%s", problem)
	}
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	message, file, problem := s.inlineContent(req, result)
	if problem != "" {
		return 0, fmt.Errorf("%s", problem)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.prepared[in.ID]; !ok || s.now().Unix() >= prepared.expires {
		return 0, fmt.Errorf("prepared message expired or was reset")
	}
	user := botapi.User{ID: in.UserID, FirstName: defaultFirstName, Username: defaultUsername}
	stored, _ := s.storeInlineLocked(in.ChatID, in.ChatType, user, message, file, result.ParseMode)
	return stored.msg.MessageID, nil
}

func (s *Server) simInline(w http.ResponseWriter, req *http.Request) {
	var in IncomingInlineQuery
	if !readJSON(w, req, &in) {
		return
	}
	update, id, err := s.InjectInlineQuery(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": update, "inline_query_id": id})
}
func (s *Server) simChooseInline(w http.ResponseWriter, req *http.Request) {
	var in InlineChoice
	if !readJSON(w, req, &in) {
		return
	}
	message, id, err := s.ChooseInlineResult(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"message_id": message, "inline_message_id": id})
}
func (s *Server) simInlineResults(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, 200, map[string]any{"queries": s.InlineQueries()})
}
func (s *Server) simWebAppData(w http.ResponseWriter, req *http.Request) {
	var in WebAppSubmission
	if !readJSON(w, req, &in) {
		return
	}
	update, message, err := s.InjectWebAppData(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"update_id": update, "message_id": message})
}
func (s *Server) simSharePrepared(w http.ResponseWriter, req *http.Request) {
	var in PreparedMessageShare
	if !readJSON(w, req, &in) {
		return
	}
	message, err := s.SharePreparedMessage(in)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"message_id": message})
}
