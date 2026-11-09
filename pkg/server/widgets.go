package server

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

const (
	widgetHTMLMax          = 32768
	widgetHTMLPackedMax    = 4096
	widgetDataMax          = 1024
	widgetQueryDataMax     = 64
	defaultWidgetCacheTime = 30
)

type sharedWidget struct {
	id           string
	html         string
	width        int
	height       int
	publicData   string
	allowQueries bool
	cacheTime    int
}

type widgetAnswer struct {
	answered bool
	data     string
}

func widgetHTMLProblem(html string) string {
	if html == "" || !utf8.ValidString(html) || strings.ContainsRune(html, '\x00') {
		return "Bad Request: INPUT_MEDIA_WIDGET_HTML_INVALID"
	}
	if len(html) > widgetHTMLMax {
		return "Bad Request: INPUT_MEDIA_WIDGET_TOO_LARGE"
	}
	var packed bytes.Buffer
	writer, err := zlib.NewWriterLevel(&packed, zlib.BestCompression)
	if err != nil {
		return "Bad Request: INPUT_MEDIA_WIDGET_HTML_INVALID"
	}
	_, writeErr := writer.Write([]byte(html))
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil || packed.Len() > widgetHTMLPackedMax {
		return "Bad Request: INPUT_MEDIA_WIDGET_TOO_LARGE"
	}
	return ""
}

func widgetDataProblem(params url.Values) string {
	for _, field := range []string{"public_data", "chat_data"} {
		if len(params.Get(field)) > widgetDataMax {
			return "Bad Request: INPUT_MEDIA_WIDGET_DATA_INVALID"
		}
	}
	return ""
}

func (s *Server) sendWidget(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	sharedSend := strings.EqualFold(method, "sendSharedWidget")
	problem := widgetDataProblem(params)
	if !sharedSend && problem == "" {
		problem = widgetHTMLProblem(params.Get("html"))
	}
	if problem == "" && utf8.RuneCountInString(params.Get("caption")) > mediaCaptionMax {
		problem = "Bad Request: message caption is too long"
	}
	var shared *sharedWidget
	if sharedSend && problem == "" {
		s.mu.Lock()
		shared = copySharedWidget(s.sharedWidgets[params.Get("widget_id")])
		s.mu.Unlock()
		if shared == nil {
			problem = "Bad Request: widget not found"
		}
	}
	markup, keyboardProblem := s.keyboardFor(chatID, params)
	if problem == "" {
		problem = keyboardProblem
	}
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}

	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	quoted, problem := replyTargetLocked(chat, params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	allowQueries := truthy(params.Get("allow_queries"))
	var widget *botapi.Widget
	if sharedSend {
		shared = copySharedWidget(s.sharedWidgets[params.Get("widget_id")])
		if shared == nil {
			s.mu.Unlock()
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: widget not found", 0)
			return
		}
		widget = &botapi.Widget{ID: shared.id, Width: shared.width, Height: shared.height, HTML: shared.html, PublicData: shared.publicData, ChatData: params.Get("chat_data")}
		allowQueries = shared.allowQueries
	} else {
		widget = &botapi.Widget{Width: atoi(params.Get("width")), Height: atoi(params.Get("height")), HTML: params.Get("html"), PublicData: params.Get("public_data"), ChatData: params.Get("chat_data")}
		if truthy(params.Get("is_shared")) {
			s.nextWidget++
			id := "widget-" + strconv.Itoa(s.nextWidget)
			widget.ID = id
			s.sharedWidgets[id] = &sharedWidget{id: id, html: widget.HTML, width: widget.Width, height: widget.Height, publicData: widget.PublicData, allowQueries: allowQueries, cacheTime: defaultWidgetCacheTime}
		}
	}
	message := &botapi.Message{
		From: s.botUser(), Chat: chat.wire(), Date: s.now().Unix(), Widget: widget,
		Caption: params.Get("caption"), ShowCaptionAboveMedia: truthy(params.Get("show_caption_above_media")),
		ReplyToMessage: quoted, ReplyMarkup: markup, HasProtectedContent: truthy(params.Get("protect_content")),
	}
	if raw := params.Get("caption_entities"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &message.CaptionEntities)
	}
	stored := chat.appendLocked(message, true, params.Get("parse_mode"), false)
	stored.widgetAllowQueries = allowQueries
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

func copySharedWidget(widget *sharedWidget) *sharedWidget {
	if widget == nil {
		return nil
	}
	copy := *widget
	return &copy
}

func (s *Server) editWidgetData(w http.ResponseWriter, method string, params url.Values) {
	if strings.EqualFold(method, "editSharedWidgetData") {
		s.editSharedWidgetData(w, method, params)
		return
	}
	public, hasPublic := params["public_data"]
	chatData, hasChat := params["chat_data"]
	problem := ""
	if (hasPublic && len(firstValue(public)) > widgetDataMax) || (hasChat && len(firstValue(chatData)) > widgetDataMax) {
		problem = "Bad Request: INPUT_MEDIA_WIDGET_DATA_INVALID"
	}
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.mu.Lock()
	target, _, problem := s.editTargetLocked(params)
	if problem == "" && (target.msg.Widget == nil || target.widgetForward) {
		problem = "Bad Request: message can't be edited"
	}
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	newPublic := firstValue(public)
	newChat := firstValue(chatData)
	changed := (hasPublic && newPublic != target.msg.Widget.PublicData) || (hasChat && newChat != target.msg.Widget.ChatData)
	if !changed {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message is not modified", 0)
		return
	}
	if hasChat {
		target.msg.Widget.ChatData = newChat
	}
	if hasPublic {
		if id := target.msg.Widget.ID; id != "" {
			s.setSharedPublicDataLocked(id, newPublic)
		} else {
			target.msg.Widget.PublicData = newPublic
		}
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (s *Server) editSharedWidgetData(w http.ResponseWriter, method string, params url.Values) {
	public, hasPublic := params["public_data"]
	if !hasPublic {
		s.writeError(w, method, params, http.StatusBadRequest, `Bad Request: parameter "public_data" is required`, 0)
		return
	}
	if len(firstValue(public)) > widgetDataMax {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: INPUT_MEDIA_WIDGET_DATA_INVALID", 0)
		return
	}
	cacheTime := 0
	if raw := params.Get("cache_time"); raw != "" {
		cacheTime = atoi(raw)
		if cacheTime < 5 {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: CACHE_TIME_INVALID", 0)
			return
		}
		cacheTime = min(cacheTime, 3600)
	}
	s.mu.Lock()
	shared := s.sharedWidgets[params.Get("widget_id")]
	if shared == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: widget not found", 0)
		return
	}
	newPublic := firstValue(public)
	changed := newPublic != shared.publicData || (cacheTime != 0 && cacheTime != shared.cacheTime)
	if !changed {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message is not modified", 0)
		return
	}
	if cacheTime != 0 {
		shared.cacheTime = cacheTime
	}
	s.setSharedPublicDataLocked(shared.id, newPublic)
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) setSharedPublicDataLocked(id, data string) {
	if shared := s.sharedWidgets[id]; shared != nil {
		shared.publicData = data
	}
	for _, chat := range s.chats {
		for _, message := range chat.messages {
			if message.msg.Widget != nil && message.msg.Widget.ID == id {
				message.msg.Widget.PublicData = data
			}
		}
	}
	for _, message := range s.inlineMessages {
		if message.msg.Widget != nil && message.msg.Widget.ID == id {
			message.msg.Widget.PublicData = data
		}
	}
}

func (s *Server) answerWidgetQuery(w http.ResponseWriter, method string, params url.Values) {
	if len(params.Get("data")) > widgetDataMax {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: data is too long", 0)
		return
	}
	s.mu.Lock()
	answer := s.widgetQueries[params.Get("widget_query_id")]
	if answer == nil || answer.answered {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: query is too old and response timeout expired or query ID is invalid", 0)
		return
	}
	answer.answered = true
	answer.data = params.Get("data")
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// WidgetAnswer reads the answer a bot gave to a widget query. ok is false for
// an unknown query, and answered is false while the bot has not answered yet.
func (s *Server) WidgetAnswer(queryID string) (data string, answered, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	answer := s.widgetQueries[queryID]
	if answer == nil {
		return "", false, false
	}
	return answer.data, answer.answered, true
}

// IncomingWidgetQuery is TelegramWidget.request from a person viewing a
// widget. With no message id the newest widget in the chat is used.
type IncomingWidgetQuery struct {
	ChatID          int64  `json:"chat_id,omitempty"`
	UserID          int64  `json:"user_id,omitempty"`
	Username        string `json:"username,omitempty"`
	FirstName       string `json:"first_name,omitempty"`
	MessageID       int    `json:"message_id,omitempty"`
	InlineMessageID string `json:"inline_message_id,omitempty"`
	Data            string `json:"data"`
}

// InjectWidgetQuery delivers widget_query, or shared_widget_query for a
// forward of a shared widget, and returns the update and query ids.
func (s *Server) InjectWidgetQuery(in IncomingWidgetQuery) (updateID int, queryID string, err error) {
	if len(in.Data) == 0 || len(in.Data) > widgetQueryDataMax {
		return 0, "", fmt.Errorf("data must contain 1-64 bytes")
	}
	identity := IncomingMessage{ChatID: in.ChatID, UserID: in.UserID, Username: in.Username, FirstName: in.FirstName}
	identity.normalize()
	in.ChatID = identity.ChatID
	user := botapi.User{ID: identity.UserID, FirstName: identity.FirstName, Username: identity.Username}

	s.mu.Lock()
	defer s.mu.Unlock()
	var target *storedMessage
	if in.InlineMessageID != "" {
		target = s.inlineMessages[in.InlineMessageID]
	} else if chat := s.chats[in.ChatID]; chat != nil {
		if in.MessageID != 0 {
			target = chat.byID[in.MessageID]
		} else {
			for i := len(chat.messages) - 1; i >= 0; i-- {
				if chat.messages[i].msg.Widget != nil && !chat.messages[i].deleted {
					target = chat.messages[i]
					break
				}
			}
		}
	}
	if target == nil || target.deleted || target.msg.Widget == nil {
		return 0, "", fmt.Errorf("widget message not found")
	}
	if !target.widgetAllowQueries || (target.widgetForward && target.msg.Widget.ID == "") {
		return 0, "", fmt.Errorf("widget queries are not allowed")
	}

	s.nextWidgetQuery++
	id := "wq-" + strconv.Itoa(s.nextWidgetQuery)
	s.widgetQueries[id] = &widgetAnswer{}
	queryType := botapi.WidgetQueryType{Type: "data"}
	if target.widgetForward {
		update := botapi.Update{SharedWidgetQuery: &botapi.SharedWidgetQuery{ID: id, From: user, Type: queryType, Data: in.Data, WidgetID: target.msg.Widget.ID}}
		return s.pushUpdateLocked(update), id, nil
	}
	query := &botapi.WidgetQuery{ID: id, From: user, Type: queryType, Data: in.Data, WidgetID: target.msg.Widget.ID, InlineMessageID: in.InlineMessageID, ChatInstance: strconv.FormatInt(target.msg.Chat.ID, 10)}
	if in.InlineMessageID != "" {
		query.ChatType = target.msg.Chat.Type
	} else {
		chat := target.msg.Chat
		query.Chat = &chat
		query.MessageID = target.msg.MessageID
	}
	return s.pushUpdateLocked(botapi.Update{WidgetQuery: query}), id, nil
}

func (s *Server) simWidgetQuery(w http.ResponseWriter, r *http.Request) {
	var in IncomingWidgetQuery
	if !readJSON(w, r, &in) {
		return
	}
	update, id, err := s.InjectWidgetQuery(in)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"update_id": update, "widget_query_id": id})
}

func (s *Server) simWidgetAnswer(w http.ResponseWriter, r *http.Request) {
	data, answered, ok := s.WidgetAnswer(r.URL.Query().Get("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "widget query not found"})
		return
	}
	var result any
	if answered && data != "" {
		result = data
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": answered, "result": result})
}
