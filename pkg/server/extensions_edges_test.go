package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestExtendedMethodRefusals(t *testing.T) {
	s := newStand(t, Options{InlineMode: true})
	s.fake.InjectMessage(IncomingMessage{Text: "user"})
	for _, tc := range []struct {
		method      string
		params      url.Values
		description string
	}{
		{"sendVideo", nil, "Bad Request: chat_id is empty"},
		{"sendVoice", url.Values{"chat_id": {"4242"}}, "Bad Request: there is no voice in the request"},
		{"sendMediaGroup", url.Values{"chat_id": {"4242"}, "media": {"[]"}}, "Bad Request: there are no messages to send"},
		{"sendMediaGroup", url.Values{"chat_id": {"4242"}, "media": {"{"}}, "Bad Request: can't parse media JSON object"},
		{"editMessageCaption", url.Values{"chat_id": {"4242"}, "message_id": {"1"}}, "Bad Request: message can't be edited"},
		{"editMessageMedia", url.Values{"chat_id": {"4242"}, "media": {"{"}}, "Bad Request: can't parse input media JSON object"},
		{"copyMessage", url.Values{"chat_id": {"2"}, "from_chat_id": {"4242"}, "message_id": {"99"}}, "Bad Request: message to copy not found"},
		{"forwardMessage", url.Values{"chat_id": {"2"}, "from_chat_id": {"4242"}, "message_id": {"99"}}, "Bad Request: message to forward not found"},
		{"deleteMessages", url.Values{"chat_id": {"4242"}, "message_ids": {"[0]"}}, "Bad Request: invalid message identifier specified"},
		{"getChat", url.Values{"chat_id": {"-999"}}, "Bad Request: chat not found"},
		{"getChatMember", url.Values{"chat_id": {"4242"}, "user_id": {"0"}}, "Bad Request: invalid user_id specified"},
		{"getChatMember", url.Values{"chat_id": {"4242"}, "user_id": {"999"}}, "Bad Request: user not found"},
		{"getUserProfilePhotos", url.Values{"user_id": {"-1"}}, "Bad Request: invalid user_id specified"},
		{"sendLocation", url.Values{"chat_id": {"4242"}}, "Bad Request: latitude is empty"},
		{"sendLocation", url.Values{"chat_id": {"4242"}, "latitude": {"1"}}, "Bad Request: longitude is empty"},
		{"sendContact", url.Values{"chat_id": {"4242"}}, "Bad Request: phone_number is empty"},
		{"answerInlineQuery", url.Values{"inline_query_id": {"missing"}, "results": {"[]"}}, invalidQuery},
		{"answerWebAppQuery", url.Values{"web_app_query_id": {"missing"}}, "Bad Request: result isn't specified"},
		{"setWebhook", url.Values{"url": {"invalid"}}, "Bad Request: invalid webhook URL specified"},
		{"setWebhook", url.Values{"url": {"http://example.com"}}, "Bad Request: bad webhook: An HTTPS URL must be provided for webhook"},
		{"setWebhook", url.Values{"url": {"https://example.com:123"}}, "Bad Request: bad webhook: Webhook can be set up only on ports 80, 88, 443 or 8443"},
		{"setWebhook", url.Values{"url": {"http://localhost"}, "secret_token": {"!"}}, "Bad Request: secret token contains illegal characters"},
		{"setWebhook", url.Values{"url": {"http://localhost"}, "secret_token": {strings.Repeat("x", 257)}}, "Bad Request: secret token is too long"},
	} {
		t.Run(tc.method+"/"+tc.description, func(t *testing.T) {
			status, body := s.call(tc.method, tc.params)
			if status != 400 || body["error_code"] != float64(400) || body["description"] != tc.description {
				t.Fatalf("%s: %d %v", tc.method, status, body)
			}
		})
	}
}

func TestMediaEditingAndAlbumFailuresPreserveState(t *testing.T) {
	s := newStand(t, Options{})
	_, sent := s.upload("sendDocument", map[string]string{"chat_id": "4242", "caption": "caption"}, "document", "a.txt", []byte("a"))
	id := result(t, sent)["document"].(map[string]any)["file_id"].(string)
	for _, method := range []string{"editMessageCaption", "editMessageMedia"} {
		params := url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "caption": {"caption"}, "media": {`{"type":"document","media":"` + id + `","caption":"caption"}`}}
		status, body := s.call(method, params)
		if status != 400 || body["description"] != notModified {
			t.Fatalf("unchanged %s: %d %v", method, status, body)
		}
	}
	status, body := s.call("sendMediaGroup", url.Values{"chat_id": {"4242"}, "media": {`[{"type":"document","media":"` + id + `"},{"type":"photo","media":"` + id + `"}]`}})
	if status != 400 || body["description"] != "Bad Request: document can't be mixed with other media types" || len(s.fake.Chat(4242).Messages) != 1 {
		t.Fatalf("mixed album: %d %v", status, body)
	}
	status, body = s.call("editMessageCaption", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "caption": {strings.Repeat("x", 1025)}})
	if status != 400 || s.fake.Chat(4242).Messages[0].Caption != "caption" {
		t.Fatalf("oversized edit changed state: %d %v", status, body)
	}
}

func TestIncomingMediaAndDownloadLimit(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.sim("POST", "/sim/media", map[string]any{"type": "document", "file_name": "in.txt", "data": []byte("incoming")})
	if status != 200 {
		t.Fatalf("media injection: %d %v", status, body)
	}
	_, poll := s.call("getUpdates", nil)
	file := updates(t, poll)[0]["message"].(map[string]any)["document"].(map[string]any)
	id := file["file_id"].(string)
	status, body = s.call("getFile", url.Values{"file_id": {id}})
	if status != 200 || result(t, body)["file_size"] != float64(8) {
		t.Fatalf("incoming getFile: %d %v", status, body)
	}
	if _, _, err := s.fake.InjectMedia(IncomingMedia{Type: "document", Data: bytes.Repeat([]byte{1}, (20<<20)+1), FileName: "large.bin"}); err != nil {
		t.Fatal(err)
	}
	status, body = s.call("getFile", url.Values{"file_id": {"file_2"}})
	if status != 400 || body["description"] != "Bad Request: file is too big" {
		t.Fatalf("large download: %d %v", status, body)
	}
}

func TestInlineMessageEditingAndPreparedRestrictions(t *testing.T) {
	s := newStand(t, Options{InlineMode: true})
	_, injected := s.sim("POST", "/sim/inline", map[string]any{"query": "hello"})
	article := `{"type":"article","id":"one","title":"Hello","input_message_content":{"message_text":"answer"},"reply_markup":{"inline_keyboard":[[{"text":"Tap","callback_data":"tap"}]]}}`
	status, body := s.call("answerInlineQuery", url.Values{"inline_query_id": {injected["inline_query_id"].(string)}, "results": {"[" + article + "]"}})
	if status != 200 {
		t.Fatalf("inline answer: %d %v", status, body)
	}
	_, choice := s.sim("POST", "/sim/inline/choose", map[string]any{"inline_query_id": injected["inline_query_id"], "result_id": "one"})
	inlineID := choice["inline_message_id"].(string)
	status, body = s.call("editMessageText", url.Values{"inline_message_id": {inlineID}, "text": {"changed"}})
	if status != 200 || body["result"] != true || !strings.Contains(s.fake.Chat(4242).Text(), "changed") {
		t.Fatalf("inline edit: %d %v", status, body)
	}
	status, body = s.call("savePreparedInlineMessage", url.Values{"user_id": {"4242"}, "result": {article}, "allow_group_chats": {"true"}})
	if status != 200 {
		t.Fatalf("prepare: %d %v", status, body)
	}
	id := result(t, body)["id"].(string)
	status, _ = s.sim("POST", "/sim/webapp/share", map[string]any{"id": id, "user_id": 999, "chat_id": -100, "chat_type": "group"})
	if status != 400 {
		t.Fatal("prepared message accepted another user")
	}
	status, _ = s.sim("POST", "/sim/webapp/share", map[string]any{"id": id})
	if status != 400 {
		t.Fatal("prepared message accepted a disallowed private chat")
	}
	status, _ = s.sim("POST", "/sim/webapp/share", map[string]any{"id": id, "chat_id": -100, "chat_type": "group"})
	if status != 200 {
		t.Fatal("prepared message rejected its allowed group")
	}
	start := s.fake.now()
	s.fake.now = func() time.Time { return start.Add(5 * time.Minute) }
	status, _ = s.sim("POST", "/sim/webapp/share", map[string]any{"id": id, "chat_id": -100, "chat_type": "group"})
	if status != 400 {
		t.Fatal("prepared message survived expiration")
	}
}

func TestWebAppDataAndProfilePhotoConfiguration(t *testing.T) {
	s := newStand(t, Options{})
	status, _ := s.sim("POST", "/sim/webapp/data", map[string]any{"data": "payload", "button_text": "Open"})
	if status != 200 {
		t.Fatal("sendData refused")
	}
	_, poll := s.call("getUpdates", nil)
	message := updates(t, poll)[0]["message"].(map[string]any)
	if message["text"] != nil || message["web_app_data"].(map[string]any)["data"] != "payload" {
		t.Fatalf("web_app_data: %v", message)
	}
	_, sent := s.upload("sendPhoto", map[string]string{"chat_id": "4242"}, "photo", "profile.png", mediaPNG(t, 3, 2))
	photo := result(t, sent)["photo"].([]any)[0]
	status, body := s.sim("POST", "/sim/user/profile", map[string]any{"user": map[string]any{"id": 4242, "first_name": "Alice"}, "photos": []any{[]any{photo}}})
	if status != 200 {
		t.Fatalf("profile: %d %v", status, body)
	}
	_, body = s.call("getUserProfilePhotos", url.Values{"user_id": {"4242"}})
	if result(t, body)["total_count"] != float64(1) || len(result(t, body)["photos"].([]any)) != 1 {
		t.Fatalf("photos: %v", body)
	}
}

func TestCloseCancelsAnInflightWebhook(t *testing.T) {
	s := newStand(t, Options{})
	started, finished := make(chan struct{}), make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = ioReadBody(req)
		close(started)
		<-req.Context().Done()
		close(finished)
	}))
	defer receiver.Close()
	s.call("setWebhook", url.Values{"url": {receiver.URL}})
	s.fake.InjectMessage(IncomingMessage{Text: "wait"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("webhook never started")
	}
	closed := make(chan struct{})
	go func() { s.fake.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for webhook timeout")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("receiver request was not cancelled")
	}
}

func ioReadBody(req *http.Request) (map[string]any, error) {
	var body map[string]any
	err := json.NewDecoder(req.Body).Decode(&body)
	return body, err
}

func TestWebhookResponseExecutesAMethod(t *testing.T) {
	s := newStand(t, Options{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = ioReadBody(req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"method":"sendMessage","chat_id":4242,"text":"webhook reply"}`))
	}))
	defer receiver.Close()
	s.call("setWebhook", url.Values{"url": {receiver.URL}})
	s.fake.InjectMessage(IncomingMessage{Text: "hello"})
	if !s.fake.WaitCall("sendMessage", 1, time.Second) || !strings.Contains(s.fake.Chat(4242).Text(), "webhook reply") {
		t.Fatal("webhook response did not call sendMessage")
	}
}

func TestCloseCancelsAMediaURLDownload(t *testing.T) {
	s := newStand(t, Options{})
	started := make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { close(started); <-req.Context().Done() }))
	defer source.Close()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	done := make(chan struct{})
	go func() {
		_, _ = s.fake.resolveMedia(request, url.Values{"document": {source.URL}}, "document", "document")
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download did not start")
	}
	s.fake.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("download did not stop on Close")
	}
}

func TestProtectedTextCannotBeForwarded(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"private"}, "protect_content": {"true"}})
	status, body := s.call("forwardMessage", url.Values{"chat_id": {"2"}, "from_chat_id": {"4242"}, "message_id": {"1"}})
	if status != 400 || body["description"] != "Bad Request: message can't be forwarded" {
		t.Fatalf("protected forward: %d %v", status, body)
	}
}

func TestInlineRequiredFieldsAreCheckedBeforeAnswering(t *testing.T) {
	s := newStand(t, Options{InlineMode: true})
	_, id, _ := s.fake.InjectInlineQuery(IncomingInlineQuery{Query: "hello"})
	for _, tc := range []struct{ result, description string }{
		{`{"type":"article","id":"a","input_message_content":{"message_text":"answer"}}`, `Bad Request: can't find field "title"`},
		{`{"type":"photo","id":"a"}`, `Bad Request: can't find field "photo_file_id"`},
		{`{"type":"location","id":"a","title":"here","latitude":100,"longitude":0}`, `Bad Request: invalid location specified`},
	} {
		status, body := s.call("answerInlineQuery", url.Values{"inline_query_id": {id}, "results": {"[" + tc.result + "]"}})
		if status != 400 || body["description"] != tc.description || s.fake.InlineQueries()[0].Answered {
			t.Fatalf("invalid inline answer: %d %v", status, body)
		}
	}
}

func TestInlineMPEG4ResultUsesTelegramFieldNames(t *testing.T) {
	s := newStand(t, Options{InlineMode: true})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("video")) }))
	defer source.Close()
	_, id, _ := s.fake.InjectInlineQuery(IncomingInlineQuery{})
	raw := `[{"type":"mpeg4_gif","id":"a","mpeg4_url":"` + source.URL + `","mpeg4_width":20,"mpeg4_height":10,"mpeg4_duration":3}]`
	status, body := s.call("answerInlineQuery", url.Values{"inline_query_id": {id}, "results": {raw}})
	if status != 200 {
		t.Fatalf("answer: %d %v", status, body)
	}
	_, _, err := s.fake.ChooseInlineResult(InlineChoice{QueryID: id, ResultID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	animation := s.fake.Chat(4242).Messages[0].Animation
	if animation == nil || animation.Width != 20 || animation.Height != 10 || animation.Duration != 3 {
		t.Fatalf("animation: %+v", animation)
	}
}

func TestInputMediaParsingRefusals(t *testing.T) {
	s := newStand(t, Options{})
	for _, tc := range []struct{ method, media, description string }{
		{"sendMediaGroup", "", `Bad Request: parameter "media" is required`},
		{"sendMediaGroup", "{}", "Bad Request: expected an Array of InputMedia"},
		{"sendMediaGroup", "null", "Bad Request: there are no messages to send"},
		{"sendMediaGroup", "[null]", "Bad Request: can't parse InputMedia: expected an Object"},
		{"editMessageMedia", "null", "Bad Request: can't parse InputMedia: expected an Object"},
		{"editMessageMedia", "{}", `Bad Request: can't parse InputMedia: Can't find field "type"`},
		{"editMessageMedia", `{"type":"photo"}`, "Bad Request: can't parse InputMedia: media not found"},
	} {
		status, body := s.call(tc.method, url.Values{"chat_id": {"4242"}, "media": {tc.media}})
		if status != 400 || body["description"] != tc.description {
			t.Fatalf("%s %s: %d %v", tc.method, tc.media, status, body)
		}
	}
}

func TestIncomingMessagesPreserveConfiguredMembership(t *testing.T) {
	s := newStand(t, Options{})
	if err := s.fake.ConfigureChat(ChatConfiguration{Chat: botapi.ChatFullInfo{"id": -100, "type": "supergroup"}, Members: []botapi.ChatMember{{"status": "creator", "user": map[string]any{"id": 4242, "first_name": "Alice"}}}}); err != nil {
		t.Fatal(err)
	}
	s.fake.InjectMessage(IncomingMessage{ChatID: -100, ChatType: "supergroup", Text: "hello"})
	_, body := s.call("getChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"4242"}})
	if result(t, body)["status"] != "creator" {
		t.Fatalf("configured status overwritten: %v", body)
	}
}

func TestInlineAndMiniAppUsersAreKnownForProfileQueries(t *testing.T) {
	s := newStand(t, Options{InlineMode: true, Token: "123456:TOKEN"})
	_, _, _ = s.fake.InjectInlineQuery(IncomingInlineQuery{UserID: 999, FirstName: "Inline"})
	if _, err := s.fake.LaunchWebApp(WebAppLaunch{UserID: 998, URL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"998", "999"} {
		status, body := s.call("getUserProfilePhotos", url.Values{"user_id": {user}})
		if status != 200 || result(t, body)["total_count"] != float64(0) {
			t.Fatalf("profile for %s: %d %v", user, status, body)
		}
	}
}
