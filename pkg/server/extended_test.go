package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetFileDownloadAndReuse(t *testing.T) {
	s := newStand(t, Options{Token: "123456:TOKEN"})
	_, sent := s.upload("sendDocument", map[string]string{"chat_id": "4242"}, "document", "notes.txt", []byte("notes"))
	id := result(t, sent)["document"].(map[string]any)["file_id"].(string)
	status, body := s.call("getFile", url.Values{"file_id": {id}})
	if status != 200 || result(t, body)["file_size"] != float64(5) {
		t.Fatalf("getFile: %d %v", status, body)
	}
	path := result(t, body)["file_path"].(string)
	response, err := http.Get(s.srv.URL + "/file/bot123456:TOKEN/" + path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || !bytes.Equal(data, []byte("notes")) {
		t.Fatalf("download: %d %q", response.StatusCode, data)
	}
	status, body = s.call("sendDocument", url.Values{"chat_id": {"4242"}, "document": {id}})
	if status != 200 || result(t, body)["document"].(map[string]any)["file_id"] != id {
		t.Fatalf("reuse: %d %v", status, body)
	}
	for _, tc := range []struct{ id, description string }{
		{"", "Bad Request: file_id not specified"},
		{"missing", "Bad Request: wrong file_id or the file is temporarily unavailable"},
	} {
		status, body = s.call("getFile", url.Values{"file_id": {tc.id}})
		if status != 400 || body["description"] != tc.description {
			t.Fatalf("getFile %q: %d %v", tc.id, status, body)
		}
	}
	response, err = http.Get(s.srv.URL + "/file/botwrong/" + path)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("file download accepted another token")
	}
	s.fake.Reset()
	status, _ = s.call("getFile", url.Values{"file_id": {id}})
	if status != 400 {
		t.Fatal("reset kept the file")
	}
}

func TestMoreMediaKindsAndURLUploads(t *testing.T) {
	for method, field := range map[string]string{"sendVideo": "video", "sendAudio": "audio", "sendVoice": "voice", "sendAnimation": "animation", "sendVideoNote": "video_note"} {
		t.Run(method, func(t *testing.T) {
			s := newStand(t, Options{})
			status, body := s.upload(method, map[string]string{"chat_id": "4242", "duration": "3", "width": "20", "height": "10", "length": "20"}, field, "media.bin", []byte("media"))
			if status != 200 || result(t, body)[field] == nil {
				t.Fatalf("%s: %d %v", method, status, body)
			}
			if !strings.Contains(s.fake.Chat(4242).Text(), "["+field+" ") {
				t.Fatal("media missing from transcript")
			}
		})
	}
	s := newStand(t, Options{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "downloaded") }))
	defer source.Close()
	status, body := s.call("sendDocument", url.Values{"chat_id": {"4242"}, "document": {source.URL + "/notes.txt"}})
	if status != 200 || result(t, body)["document"] == nil {
		t.Fatalf("URL document: %d %v", status, body)
	}
	status, body = s.upload("sendDocument", map[string]string{"chat_id": "4242", "document": "attach://upload"}, "upload", "attached.txt", []byte("attached"))
	if status != 200 {
		t.Fatalf("attach: %d %v", status, body)
	}
}

func TestMediaGroupIsAtomic(t *testing.T) {
	s := newStand(t, Options{})
	_, uploaded := s.upload("sendPhoto", map[string]string{"chat_id": "1"}, "photo", "a.png", mediaPNG(t, 2, 2))
	id := result(t, uploaded)["photo"].([]any)[0].(map[string]any)["file_id"].(string)
	media := `[{"type":"photo","media":"` + id + `","caption":"one"},{"type":"photo","media":"` + id + `","caption":"two"}]`
	status, body := s.call("sendMediaGroup", url.Values{"chat_id": {"4242"}, "media": {media}})
	if status != 200 {
		t.Fatalf("album: %d %v", status, body)
	}
	batch := body["result"].([]any)
	if len(batch) != 2 || batch[0].(map[string]any)["media_group_id"] == "" || batch[0].(map[string]any)["media_group_id"] != batch[1].(map[string]any)["media_group_id"] {
		t.Fatalf("album: %v", batch)
	}
	status, body = s.call("sendMediaGroup", url.Values{"chat_id": {"4242"}, "media": {`[{"type":"photo","media":"` + id + `"},{"type":"photo","media":"missing"}]`}})
	if status != 400 || len(s.fake.Chat(4242).Messages) != 2 {
		t.Fatalf("partial album stored: %d %v", status, body)
	}
}

func TestMediaEditsCopyAndForward(t *testing.T) {
	s := newStand(t, Options{})
	_, sent := s.upload("sendDocument", map[string]string{"chat_id": "4242", "caption": "old"}, "document", "notes.txt", []byte("notes"))
	id := result(t, sent)["document"].(map[string]any)["file_id"].(string)
	status, body := s.call("editMessageCaption", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "caption": {"new"}})
	if status != 200 || result(t, body)["caption"] != "new" {
		t.Fatalf("caption: %d %v", status, body)
	}
	status, body = s.call("editMessageMedia", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "media": {`{"type":"document","media":"` + id + `","caption":"replaced"}`}})
	if status != 200 || result(t, body)["caption"] != "replaced" {
		t.Fatalf("edit media: %d %v", status, body)
	}
	status, body = s.call("copyMessage", url.Values{"chat_id": {"2"}, "from_chat_id": {"4242"}, "message_id": {"1"}, "caption": {"copy"}})
	if status != 200 || result(t, body)["message_id"] != float64(1) || s.fake.Chat(2).Messages[0].Caption != "copy" {
		t.Fatalf("copy: %d %v", status, body)
	}
	status, body = s.call("forwardMessage", url.Values{"chat_id": {"2"}, "from_chat_id": {"4242"}, "message_id": {"1"}})
	if status != 200 || result(t, body)["forward_origin"] == nil {
		t.Fatalf("forward: %d %v", status, body)
	}
	status, body = s.call("deleteMessages", url.Values{"chat_id": {"2"}, "message_ids": {`[1,2,999]`}})
	if status != 200 || s.fake.Chat(2).Text() != "" {
		t.Fatalf("deleteMessages: %d %v", status, body)
	}
}

func TestInlineAndWebAppQueries(t *testing.T) {
	s := newStand(t, Options{Token: "123456:TOKEN", InlineMode: true})
	status, injected := s.sim("POST", "/sim/inline", map[string]any{"query": "hello"})
	if status != 200 {
		t.Fatalf("inline injection: %d %v", status, injected)
	}
	query := injected["inline_query_id"].(string)
	article := `{"type":"article","id":"one","title":"Hello","input_message_content":{"message_text":"inline answer"}}`
	status, body := s.call("answerInlineQuery", url.Values{"inline_query_id": {query}, "results": {"[" + article + "]"}})
	if status != 200 {
		t.Fatalf("answerInlineQuery: %d %v", status, body)
	}
	status, chosen := s.sim("POST", "/sim/inline/choose", map[string]any{"inline_query_id": query, "result_id": "one"})
	if status != 200 || !strings.Contains(s.fake.Chat(4242).Text(), "inline answer") {
		t.Fatalf("choose: %d %v", status, chosen)
	}
	_, poll := s.call("getUpdates", nil)
	batch := updates(t, poll)
	if len(batch) != 2 || batch[0]["inline_query"] == nil || batch[1]["chosen_inline_result"] == nil {
		t.Fatalf("inline updates: %v", batch)
	}
	status, body = s.call("answerInlineQuery", url.Values{"inline_query_id": {query}, "results": {"[]"}})
	if status != 400 || body["description"] != queryInvalid {
		t.Fatalf("query reused: %d %v", status, body)
	}
	launch, err := s.fake.LaunchWebApp(WebAppLaunch{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := url.ParseQuery(launch.InitData)
	status, body = s.call("answerWebAppQuery", url.Values{"web_app_query_id": {data.Get("query_id")}, "result": {article}})
	if status != 200 || result(t, body)["inline_message_id"] == "" {
		t.Fatalf("WebApp answer: %d %v", status, body)
	}
	status, body = s.call("answerWebAppQuery", url.Values{"web_app_query_id": {data.Get("query_id")}, "result": {article}})
	if status != 400 || body["description"] != queryInvalid {
		t.Fatalf("WebApp query reused: %d %v", status, body)
	}
}

const queryInvalid = "Bad Request: query is too old and response timeout expired or query ID is invalid"

func TestWebhooksDeliverRetryAndBlockPolling(t *testing.T) {
	s := newStand(t, Options{})
	delivered := make(chan map[string]any, 2)
	var attempts atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Telegram-Bot-Api-Secret-Token") != "secret" {
			t.Error("missing webhook secret")
		}
		var update map[string]any
		_ = json.NewDecoder(req.Body).Decode(&update)
		if attempts.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		delivered <- update
		w.WriteHeader(200)
	}))
	defer receiver.Close()
	status, body := s.call("setWebhook", url.Values{"url": {receiver.URL}, "secret_token": {"secret"}})
	if status != 200 {
		t.Fatalf("setWebhook: %d %v", status, body)
	}
	status, body = s.call("getUpdates", nil)
	if status != 409 {
		t.Fatalf("poll with webhook: %d %v", status, body)
	}
	s.fake.InjectMessage(IncomingMessage{Text: "webhook"})
	select {
	case update := <-delivered:
		if update["message"].(map[string]any)["text"] != "webhook" {
			t.Fatalf("update: %v", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook did not retry and deliver")
	}
	_, body = s.call("getWebhookInfo", nil)
	if result(t, body)["url"] != receiver.URL {
		t.Fatalf("webhook info: %v", body)
	}
	status, body = s.call("deleteWebhook", url.Values{"drop_pending_updates": {"true"}})
	if status != 200 {
		t.Fatalf("deleteWebhook: %d %v", status, body)
	}
	status, body = s.call("getUpdates", nil)
	if status != 200 || len(updates(t, body)) != 0 {
		t.Fatalf("poll after delete: %d %v", status, body)
	}
}

func TestChatInformationUsesConfiguredState(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.sim("POST", "/sim/chat/configure", map[string]any{
		"chat": map[string]any{"id": -100, "type": "supergroup", "title": "Test group", "username": "group", "description": "demo"},
		"members": []map[string]any{
			{"status": "creator", "user": map[string]any{"id": 4242, "is_bot": false, "first_name": "Alice"}, "is_anonymous": false},
			{"status": "administrator", "user": map[string]any{"id": 777, "is_bot": true, "first_name": "Helper"}},
		},
	})
	if status != 200 {
		t.Fatalf("configure: %d %v", status, body)
	}
	status, body = s.call("getChat", url.Values{"chat_id": {"@group"}})
	if status != 200 || result(t, body)["title"] != "Test group" || result(t, body)["description"] != "demo" {
		t.Fatalf("getChat: %d %v", status, body)
	}
	status, body = s.call("getChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"4242"}})
	if status != 200 || result(t, body)["status"] != "creator" {
		t.Fatalf("member: %d %v", status, body)
	}
	status, body = s.call("getChatAdministrators", url.Values{"chat_id": {"-100"}})
	admins, _ := body["result"].([]any)
	if status != 200 || len(admins) != 2 {
		t.Fatalf("admins: %d %v", status, body)
	}
	if admins[0].(map[string]any)["user"].(map[string]any)["id"] != float64(777) {
		t.Fatalf("the bot administrator is missing: %v", admins)
	}
	status, body = s.call("getChatMemberCount", url.Values{"chat_id": {"-100"}})
	if status != 200 || body["result"] != float64(3) {
		t.Fatalf("member count: %d %v", status, body)
	}
}
