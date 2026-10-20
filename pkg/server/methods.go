package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
	"github.com/EvilFreelancer/tgfake/pkg/webapp"
)

// serveBotAPI answers /bot<token>/<method>. Anything else under / is a 404
// in the Bot API's own envelope, which is what api.telegram.org answers for
// an unknown path too.
func (s *Server) serveBotAPI(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	if !ok {
		s.writeError(w, "", nil, http.StatusNotFound, "Not Found", 0)
		return
	}
	token, method, found := strings.Cut(rest, "/")
	if !found || method == "" {
		s.writeError(w, "", nil, http.StatusNotFound, "Not Found", 0)
		return
	}
	method = strings.TrimSuffix(method, "/")
	params := parseParams(r)
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	if s.opts.Token != "" && token != s.opts.Token {
		s.writeError(w, method, params, http.StatusUnauthorized, "Unauthorized", 0)
		return
	}
	if s.opts.Token == "" {
		s.mu.Lock()
		s.token = token
		s.mu.Unlock()
	}
	if fault := s.takeFault(method, params); fault != nil {
		s.writeError(w, method, params, fault.Code, fault.Description, fault.RetryAfter)
		return
	}
	switch strings.ToLower(method) {
	case "getme":
		s.writeResult(w, method, params, s.me())
	case "getupdates":
		s.getUpdates(w, r, method, params)
	case "setmycommands":
		s.setMyCommands(w, method, params)
	case "getmycommands":
		s.getMyCommands(w, method, params)
	case "deletemycommands":
		s.deleteMyCommands(w, method, params)
	case "sendmessage":
		s.sendMessage(w, method, params)
	case "sendphoto":
		s.sendMedia(w, r, method, params, "photo")
	case "senddocument":
		s.sendMedia(w, r, method, params, "document")
	case "sendvideo", "sendaudio", "sendvoice", "sendanimation", "sendvideonote":
		field := strings.TrimPrefix(strings.ToLower(method), "send")
		if field == "videonote" {
			field = "video_note"
		}
		s.sendMedia(w, r, method, params, field)
	case "getfile":
		s.getFile(w, method, params)
	case "sendmediagroup":
		s.sendMediaGroup(w, r, method, params)
	case "editmessagecaption", "editmessagemedia":
		s.editMedia(w, r, method, params)
	case "copymessage", "forwardmessage", "copymessages", "forwardmessages":
		s.relayMessages(w, method, params)
	case "deletemessages":
		s.deleteMessages(w, method, params)
	case "sendlocation", "sendvenue", "sendcontact":
		s.sendContent(w, method, params)
	case "answerinlinequery", "answerwebappquery", "savepreparedinlinemessage":
		s.answerInline(w, r, method, params)
	case "getchat", "getchatmember", "getchatmembercount", "getchatadministrators", "getuserprofilephotos":
		s.chatInformation(w, method, params)
	case "editmessagetext", "editmessagereplymarkup":
		s.editMessage(w, method, params)
	case "deletemessage":
		s.deleteMessage(w, method, params)
	case "sendchataction":
		s.sendChatAction(w, method, params)
	case "answercallbackquery":
		s.answerCallbackQuery(w, method, params)
	case "sendinvoice":
		s.sendInvoice(w, method, params)
	case "answerprecheckoutquery":
		s.answerPreCheckoutQuery(w, method, params)
	case "sendrichmessage":
		s.sendRichMessage(w, method, params)
	case "sendrichmessagedraft":
		s.sendRichMessageDraft(w, method, params)
	case "setchatmenubutton":
		s.setChatMenuButton(w, method, params)
	case "getchatmenubutton":
		s.getChatMenuButton(w, method, params)
	case "setwebhook", "deletewebhook":
		s.setWebhook(w, r, method, params)
	case "getwebhookinfo":
		s.webhookInfo(w, method, params)
	default:
		s.writeError(w, method, params, http.StatusNotFound, "Not Found: method not found", 0)
	}
}

// parseParams reads the request the way the Bot API does: query string and
// urlencoded form, a JSON object whose nested values stay JSON text - the
// form a library encodes reply_markup and rich_message in anyway - or a
// multipart form, whose file parts stay on the request for the method to
// read while the fields land in the same values.
func parseParams(r *http.Request) url.Values {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		_ = r.ParseMultipartForm(32 << 20)
		return r.Form
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		out := url.Values{}
		for k, v := range r.URL.Query() {
			out[k] = v
		}
		var doc map[string]json.RawMessage
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if json.Unmarshal(body, &doc) == nil {
			for k, raw := range doc {
				var str string
				if json.Unmarshal(raw, &str) == nil {
					out.Set(k, str)
				} else {
					out.Set(k, string(raw))
				}
			}
		}
		return out
	}
	_ = r.ParseForm()
	return r.Form
}

func (s *Server) takeFault(method string, params url.Values) *Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeFaultLocked(method, params)
}

func (s *Server) me() map[string]any {
	return map[string]any{
		"id":                          s.opts.BotID,
		"is_bot":                      true,
		"first_name":                  s.opts.BotFirstName,
		"username":                    s.opts.BotUsername,
		"can_join_groups":             true,
		"can_read_all_group_messages": false,
		"supports_inline_queries":     s.opts.InlineMode,
	}
}

// getUpdates confirms what the client has seen and answers with what is
// pending, holding the request open up to the smaller of the client's
// timeout and MaxPollWait when there is nothing yet. allowed_updates works
// as on api.telegram.org: a list replaces the subscription, an empty list
// means every kind, and none at all (or the literal null some libraries
// send) keeps whatever the previous poll asked for.
func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request, method string, params url.Values) {
	s.mu.Lock()
	activeWebhook := s.webhook.url != ""
	s.mu.Unlock()
	if activeWebhook {
		s.writeError(w, method, params, 409, "Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first", 0)
		return
	}
	offset := atoi(params.Get("offset"))
	limit := atoi(params.Get("limit"))
	if raw := strings.TrimSpace(params.Get("allowed_updates")); raw != "" && raw != "null" {
		var kinds []string
		if err := json.Unmarshal([]byte(raw), &kinds); err != nil {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse allowed_updates JSON array", 0)
			return
		}
		s.SetAllowedUpdates(kinds)
	}
	wait := time.Duration(atoi(params.Get("timeout"))) * time.Second
	if wait > s.opts.MaxPollWait {
		wait = s.opts.MaxPollWait
	}
	deadline := time.Now().Add(wait)
	for {
		s.mu.Lock()
		if s.webhook.url != "" {
			s.mu.Unlock()
			s.writeError(w, method, params, 409, "Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first", 0)
			return
		}
		batch := s.takeUpdatesLocked(offset, limit)
		wake := s.wake
		s.mu.Unlock()
		if len(batch) > 0 || wait <= 0 || !time.Now().Before(deadline) || s.closed() {
			s.writeResult(w, method, params, batch)
			return
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-wake:
		case <-timer.C:
		case <-s.closing:
		case <-r.Context().Done():
			// The client went away; nothing to answer.
			timer.Stop()
			return
		}
		timer.Stop()
	}
}

// closed reports whether Close was called.
func (s *Server) closed() bool {
	select {
	case <-s.closing:
		return true
	default:
		return false
	}
}

func (s *Server) sendMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	text := params.Get("text")
	if text == "" {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message text is empty", 0)
		return
	}
	if utf8.RuneCountInString(text) > messageTextMax {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message is too long", 0)
		return
	}
	markup, problem := s.keyboardFor(chatID, params)
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
	msg := &botapi.Message{
		From:           s.botUser(),
		Chat:           chat.wire(),
		Date:           s.now().Unix(),
		Text:           text,
		ReplyToMessage: quoted,
	}
	msg.ReplyMarkup = markup
	msg.HasProtectedContent = truthy(params.Get("protect_content"))
	stored := chat.appendLocked(msg, true, params.Get("parse_mode"), false)
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

// keyboardFor reads and checks the reply_markup of a message to chatID: the
// inline keyboard, or "" when there is none, and Telegram's refusal when it
// would not take it.
func (s *Server) keyboardFor(chatID int64, params url.Values) (*botapi.InlineKeyboardMarkup, string) {
	markup, problem := parseKeyboard(params.Get("reply_markup"))
	if problem != "" {
		return nil, problem
	}
	s.mu.Lock()
	chatType := s.chatTypeLocked(chatID)
	s.mu.Unlock()
	if problem := validateKeyboard(markup, chatType, false); problem != "" {
		return nil, problem
	}
	return markup, ""
}

// mediaCaptionMax is Telegram's limit on the caption of a media message, in
// characters.
const mediaCaptionMax = 1024

// photoMaxSize and documentMaxSize are the upload limits of sendPhoto and
// sendDocument, matching what api.telegram.org accepts from a bot.
const (
	photoMaxSize    = 10 << 20
	documentMaxSize = 50 << 20
)

func (s *Server) editMessage(w http.ResponseWriter, method string, params url.Values) {
	s.mu.Lock()
	target, inline, problem := s.editTargetLocked(params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	newText := target.msg.Text
	if strings.EqualFold(method, "editMessageText") {
		if target.file != nil || target.msg.Location != nil || target.msg.Contact != nil {
			s.mu.Unlock()
			s.writeError(w, method, params, 400, "Bad Request: there is no text in the message to edit", 0)
			return
		}
		newText = params.Get("text")
		if newText == "" {
			s.mu.Unlock()
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message text is empty", 0)
			return
		}
		if utf8.RuneCountInString(newText) > messageTextMax {
			s.mu.Unlock()
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message is too long", 0)
			return
		}
	}
	newMarkup, problem := parseKeyboard(params.Get("reply_markup"))
	invoice := target.msg.Invoice != nil
	if problem == "" {
		problem = validateKeyboard(newMarkup, target.msg.Chat.Type, invoice)
	}
	if problem == "" && invoice {
		// An invoice keeps the rule sendInvoice applied: a keyboard leads
		// with its Pay button.
		problem = invoiceKeyboardProblem(newMarkup)
	}
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	if newText == target.msg.Text && sameKeyboard(newMarkup, target.msg.ReplyMarkup) {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest,
			"Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message", 0)
		return
	}
	target.msg.Text = newText
	target.msg.ReplyMarkup = newMarkup
	target.msg.EditDate = s.now().Unix()
	target.edited = true
	if pm := params.Get("parse_mode"); strings.EqualFold(method, "editMessageText") {
		target.parseMode = pm
	}
	result := target.clone()
	s.mu.Unlock()
	if inline {
		s.writeResult(w, method, params, true)
	} else {
		s.writeResult(w, method, params, result)
	}
}

func (s *Server) deleteMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	msgID := atoi(params.Get("message_id"))
	s.mu.Lock()
	chat := s.chats[chatID]
	var target *storedMessage
	if chat != nil {
		target = chat.byID[msgID]
	}
	if target == nil || target.deleted {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message to delete not found", 0)
		return
	}
	target.deleted = true
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) sendChatAction(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	chat.lastTyped = s.now()
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) answerCallbackQuery(w http.ResponseWriter, method string, params url.Values) {
	id := params.Get("callback_query_id")
	if id == "" {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: callback_query_id is empty", 0)
		return
	}
	answer := CallbackAnswer{ID: id, Text: params.Get("text"), ShowAlert: truthy(params.Get("show_alert"))}
	s.mu.Lock()
	// The query names no chat; the tap that minted it does. A query the fake
	// never issued, one minted before a Reset and one already answered are
	// refused the way Telegram refuses an answer to a query it does not know.
	chatID, known := s.cbqChat[id]
	if !known {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest,
			"Bad Request: query is too old and response timeout expired or query ID is invalid", 0)
		return
	}
	if chat := s.chats[chatID]; chat != nil {
		chat.callbacks = append(chat.callbacks, answer)
	}
	// A query takes one answer, so it is forgotten once answered: a bot that
	// acknowledges a tap at once and sends an alert about a later failure
	// loses that alert on Telegram, and the stand has to lose it too.
	delete(s.cbqChat, id)
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// richInput is the InputRichMessage object of Bot API 10.1.
type richInput struct {
	Markdown string `json:"markdown"`
	HTML     string `json:"html"`
}

func (s *Server) sendRichMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	var in richInput
	if err := json.Unmarshal([]byte(params.Get("rich_message")), &in); err != nil || (in.Markdown == "" && in.HTML == "") {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse rich_message JSON object", 0)
		return
	}
	body := in.Markdown
	if body == "" {
		body = in.HTML
	}
	markup, problem := s.keyboardFor(chatID, params)
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
	msg := &botapi.Message{
		From:           s.botUser(),
		Chat:           chat.wire(),
		Date:           s.now().Unix(),
		Text:           body,
		ReplyToMessage: quoted,
		ReplyMarkup:    markup,
	}
	msg.HasProtectedContent = truthy(params.Get("protect_content"))
	stored := chat.appendLocked(msg, true, "", true)
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

func (s *Server) sendRichMessageDraft(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	draftID, err := strconv.ParseInt(params.Get("draft_id"), 10, 64)
	if err != nil || draftID == 0 {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: draft_id must be a non-zero integer", 0)
		return
	}
	var in richInput
	if err := json.Unmarshal([]byte(params.Get("rich_message")), &in); err != nil {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse rich_message JSON object", 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	now := s.now()
	chat.expireDraftsLocked(now)
	d := chat.drafts[draftID]
	if d == nil {
		d = &draft{id: draftID}
		chat.drafts[draftID] = d
	}
	d.markdown = in.Markdown
	d.updatedAt = now
	d.revisions++
	d.threadID = atoi(params.Get("message_thread_id"))
	d.canStop = truthy(params.Get("can_stop"))
	d.keepOnStop = truthy(params.Get("keep_on_stop"))
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) botUser() *botapi.User {
	return &botapi.User{ID: s.opts.BotID, IsBot: true, FirstName: s.opts.BotFirstName, Username: s.opts.BotUsername}
}

// writeResult answers ok:true and files the call.
func (s *Server) writeResult(w http.ResponseWriter, method string, params url.Values, result any) {
	body, _ := json.Marshal(apiResponse{OK: true, Result: result})
	s.respond(w, method, params, http.StatusOK, body)
}

// writeError answers ok:false with the Bot API's status-as-error_code
// convention and files the call.
func (s *Server) writeError(w http.ResponseWriter, method string, params url.Values, code int, description string, retryAfter int) {
	resp := apiResponse{OK: false, ErrorCode: code, Description: description}
	if retryAfter > 0 {
		resp.Parameters = &responseParams{RetryAfter: retryAfter}
	}
	body, _ := json.Marshal(resp)
	s.respond(w, method, params, code, body)
}

func (s *Server) respond(w http.ResponseWriter, method string, params url.Values, status int, body []byte) {
	if method != "" {
		s.mu.Lock()
		s.recordLocked(method, params, status, body)
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func chatIDOf(params url.Values) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(params.Get("chat_id")), 10, 64)
	return id, err == nil && id != 0
}

// truthy reads a Boolean parameter the way the Bot API server does: "true",
// "yes" or "1" in any case and with surrounding spaces is true, anything else
// is false. Libraries differ in how they spell true ("true", "True", "1").
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true
	}
	return false
}

// messageTextMax is Telegram's limit on the text of one message, in
// characters: what makes a bot split a long answer, and what a fake that
// accepted any length would let a streaming path get wrong unnoticed.
const messageTextMax = 4096

// replyTargetLocked resolves the message a send replies to, named by
// reply_parameters (Bot API 7) or by the older reply_to_message_id. Telegram
// refuses a reply to a message that is not in the chat unless
// allow_sending_without_reply is set, in which case the message goes out
// unthreaded; the fake does the same. It returns the quote to attach, or the
// error description. Caller holds s.mu.
func replyTargetLocked(chat *chatState, params url.Values) (*botapi.Message, string) {
	var reply struct {
		MessageID                int  `json:"message_id"`
		AllowSendingWithoutReply bool `json:"allow_sending_without_reply"`
	}
	if raw := strings.TrimSpace(params.Get("reply_parameters")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &reply); err != nil {
			return nil, "Bad Request: can't parse reply parameters JSON object"
		}
	} else {
		reply.MessageID = atoi(params.Get("reply_to_message_id"))
		reply.AllowSendingWithoutReply = truthy(params.Get("allow_sending_without_reply"))
	}
	if reply.MessageID == 0 {
		return nil, ""
	}
	target := chat.byID[reply.MessageID]
	if target == nil || target.deleted {
		if reply.AllowSendingWithoutReply {
			return nil, ""
		}
		return nil, "Bad Request: message to be replied not found"
	}
	return target.quoted(), ""
}

// inlineKeyboardNotArray is what Telegram answers when reply_markup names
// inline_keyboard with a value that is not an array.
const inlineKeyboardNotArray = `Bad Request: Field "inline_keyboard" must be of type Array`

// parseKeyboard reads reply_markup; a markup of another kind (a reply
// keyboard, a remove) is not an inline keyboard and reads as none, and so does
// an empty array. An inline_keyboard of any other type, null included, is
// refused the way the Bot API server refuses it: that null is what a library
// sends for a keyboard built from no rows, and a real chat never shows the
// message it came with. It returns the keyboard, or the error description.
func parseKeyboard(raw string) (*botapi.InlineKeyboardMarkup, string) {
	if strings.TrimSpace(raw) == "" {
		return nil, ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, ""
	}
	value, ok := fields["inline_keyboard"]
	if !ok {
		return nil, ""
	}
	if trimmed := strings.TrimSpace(string(value)); !strings.HasPrefix(trimmed, "[") {
		return nil, inlineKeyboardNotArray
	}
	var kb botapi.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(raw), &kb); err != nil || len(kb.InlineKeyboard) == 0 {
		return nil, ""
	}
	return &kb, ""
}

// callbackDataMax is Telegram's limit on a button's callback_data, in bytes.
const callbackDataMax = 64

// validateKeyboard refuses what api.telegram.org refuses: a button with
// nothing behind it, callback_data over the limit - the mistake that is
// invisible until a real chat shows a keyboard that never arrives - and a
// web_app button outside a private chat or to an address Telegram would not
// open. It also refuses a button with more than one action, which Telegram
// would read as its first, and a Pay button outside an invoice. payAllowed
// says the keyboard belongs to an invoice. It returns the error description,
// or "" for a keyboard the stand takes in a chat of chatType.
func validateKeyboard(kb *botapi.InlineKeyboardMarkup, chatType string, payAllowed bool) string {
	if kb == nil {
		return ""
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			actions := 0
			for _, set := range []bool{b.CallbackData != "", b.URL != "", b.WebApp != nil, b.SwitchInlineQuery != nil, b.SwitchInlineQueryCurrentChat != nil, b.CopyText != nil, b.Pay} {
				if set {
					actions++
				}
			}
			switch {
			case actions == 0:
				return "Bad Request: Text buttons are not allowed in the inline keyboard"
			case actions > 1:
				// Telegram takes the first action in its own order; the
				// stand refuses the button, so an ambiguous one never passes.
				return "Bad Request: BUTTON_TYPE_INVALID"
			}
			if b.Pay && !payAllowed {
				return "Bad Request: BUTTON_TYPE_INVALID"
			}
			if len(b.CallbackData) > callbackDataMax {
				return "Bad Request: BUTTON_DATA_INVALID"
			}
			if b.CopyText != nil && (utf8.RuneCountInString(b.CopyText.Text) < 1 || utf8.RuneCountInString(b.CopyText.Text) > 256) {
				return "Bad Request: BUTTON_COPY_TEXT_INVALID"
			}
			if b.WebApp != nil {
				if chatType != "private" {
					return "Bad Request: BUTTON_TYPE_INVALID"
				}
				if problem := webapp.URLProblem(b.WebApp.URL); problem != "" {
					return "Bad Request: inline keyboard button " + problem
				}
			}
		}
	}
	return ""
}

func sameKeyboard(a, b *botapi.InlineKeyboardMarkup) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a.InlineKeyboard) != len(b.InlineKeyboard) {
		return false
	}
	for i := range a.InlineKeyboard {
		if len(a.InlineKeyboard[i]) != len(b.InlineKeyboard[i]) {
			return false
		}
		for j := range a.InlineKeyboard[i] {
			x, y := a.InlineKeyboard[i][j], b.InlineKeyboard[i][j]
			if !reflect.DeepEqual(x, y) {
				return false
			}
		}
	}
	return true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
