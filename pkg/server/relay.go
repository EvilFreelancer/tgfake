package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func messageIDs(raw string) ([]int, string) {
	if raw == "" {
		return nil, "Bad Request: message identifiers are not specified"
	}
	var values []json.RawMessage
	if json.Unmarshal([]byte(raw), &values) != nil {
		if json.Valid([]byte(raw)) {
			return nil, "Bad Request: expected an Array of message identifiers"
		}
		return nil, "Bad Request: can't parse message_ids JSON object"
	}
	if values == nil {
		return nil, "Bad Request: expected an Array of message identifiers"
	}
	if len(values) > 100 {
		return nil, "Bad Request: too many message identifiers specified"
	}
	if len(values) == 0 {
		return nil, "Bad Request: message identifiers are not specified"
	}
	ids := make([]int, 0, len(values))
	for _, raw := range values {
		value := string(raw)
		var str string
		if json.Unmarshal(raw, &str) == nil {
			value = str
		} else if len(value) == 0 || (value[0] != '-' && (value[0] < '0' || value[0] > '9')) {
			return nil, "Bad Request: message identifier must be a Number"
		}
		id, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return nil, "Bad Request: can't parse message identifier as a Number"
		}
		if id <= 0 {
			return nil, "Bad Request: invalid message identifier specified"
		}
		ids = append(ids, int(id))
	}
	return ids, ""
}

func (s *Server) deleteMessages(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	ids, problem := messageIDs(params.Get("message_ids"))
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.mu.Lock()
	chat := s.chats[chatID]
	if chat == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, "Bad Request: chat not found", 0)
		return
	}
	for _, id := range ids {
		if message := chat.byID[id]; message != nil {
			message.deleted = true
		}
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) relayMessages(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	fromID, err := strconv.ParseInt(params.Get("from_chat_id"), 10, 64)
	if err != nil || fromID == 0 {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	bulk := strings.HasSuffix(strings.ToLower(method), "messages")
	forward := strings.HasPrefix(strings.ToLower(method), "forward")
	ids := []int{atoi(params.Get("message_id"))}
	problem := ""
	if bulk {
		ids, problem = messageIDs(params.Get("message_ids"))
	}
	if !forward && utf8.RuneCountInString(params.Get("caption")) > mediaCaptionMax {
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
	source := s.chats[fromID]
	sources := make([]*storedMessage, 0, len(ids))
	for _, id := range ids {
		var message *storedMessage
		if source != nil {
			message = source.byID[id]
		}
		if message == nil || message.deleted {
			if bulk {
				continue
			}
			what := "copy"
			if forward {
				what = "forward"
			}
			problem = "Bad Request: message to " + what + " not found"
			break
		}
		if forward && message.msg.HasProtectedContent {
			problem = "Bad Request: message can't be forwarded"
			break
		}
		sources = append(sources, message)
	}
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	chat := s.ensureChatLocked(chatID, "", "", nil)
	quoted, problem := replyTargetLocked(chat, params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	results := make([]botapi.MessageID, 0, len(sources))
	var sent *botapi.Message
	groups := map[string]string{}
	for _, original := range sources {
		copy := original.clone()
		copy.MessageID, copy.Date, copy.EditDate = 0, s.now().Unix(), 0
		copy.From, copy.Chat, copy.ReplyToMessage = s.botUser(), chat.wire(), quoted
		copy.HasProtectedContent = params.Get("protect_content") == "true"
		copy.MediaGroupID = ""
		if bulk && original.msg.MediaGroupID != "" {
			if groups[original.msg.MediaGroupID] == "" {
				s.nextAlbum++
				groups[original.msg.MediaGroupID] = strconv.Itoa(s.nextAlbum)
			}
			copy.MediaGroupID = groups[original.msg.MediaGroupID]
		}
		if forward {
			if copy.ForwardOrigin == nil {
				copy.ForwardOrigin = &botapi.MessageOrigin{Type: "user", Date: original.msg.Date, SenderUser: original.msg.From}
			}
		} else {
			copy.ForwardOrigin, copy.ViaBot = nil, nil
			copy.ReplyMarkup = markup
			if _, present := params["caption"]; present {
				copy.Caption = params.Get("caption")
			}
			if params.Get("remove_caption") == "true" {
				copy.Caption = ""
			}
		}
		stored := chat.appendLocked(copy, true, original.parseMode, original.rich)
		stored.file = original.file
		sent = stored.clone()
		results = append(results, botapi.MessageID{MessageID: sent.MessageID})
	}
	s.mu.Unlock()
	if bulk {
		s.writeResult(w, method, params, results)
	} else if forward {
		s.writeResult(w, method, params, sent)
	} else {
		s.writeResult(w, method, params, results[0])
	}
}

func contentMessage(params url.Values, kind string) (*botapi.Message, string) {
	message := &botapi.Message{}
	if kind == "sendcontact" {
		if params.Get("phone_number") == "" {
			return nil, "Bad Request: phone_number is empty"
		}
		if params.Get("first_name") == "" {
			return nil, "Bad Request: first_name is empty"
		}
		message.Contact = &botapi.Contact{PhoneNumber: params.Get("phone_number"), FirstName: params.Get("first_name"), LastName: params.Get("last_name"), VCard: params.Get("vcard")}
		return message, ""
	}
	if params.Get("latitude") == "" {
		return nil, "Bad Request: latitude is empty"
	}
	if params.Get("longitude") == "" {
		return nil, "Bad Request: longitude is empty"
	}
	latitude, latError := strconv.ParseFloat(params.Get("latitude"), 64)
	longitude, lonError := strconv.ParseFloat(params.Get("longitude"), 64)
	if latError != nil || lonError != nil || math.IsNaN(latitude) || math.IsNaN(longitude) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, "Bad Request: invalid location specified"
	}
	location := botapi.Location{Latitude: latitude, Longitude: longitude}
	if kind == "sendvenue" {
		message.Venue = &botapi.Venue{Location: location, Title: params.Get("title"), Address: params.Get("address"), FoursquareID: params.Get("foursquare_id"), FoursquareType: params.Get("foursquare_type"), GooglePlaceID: params.Get("google_place_id"), GooglePlaceType: params.Get("google_place_type")}
		message.Location = &location
	} else {
		message.Location = &location
	}
	return message, ""
}

func (s *Server) sendContent(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	message, problem := contentMessage(params, strings.ToLower(method))
	markup, kbProblem := s.keyboardFor(chatID, params)
	if problem == "" {
		problem = kbProblem
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
	message.From, message.Chat, message.Date, message.ReplyToMessage, message.ReplyMarkup = s.botUser(), chat.wire(), s.now().Unix(), quoted, markup
	message.HasProtectedContent = params.Get("protect_content") == "true"
	stored := chat.appendLocked(message, true, "", false)
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}
