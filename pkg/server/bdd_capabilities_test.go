package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestBotCapabilitiesFeature(t *testing.T) {
	article := `{"type":"article","id":"one","title":"Hello","input_message_content":{"message_text":"inline answer"}}`
	suite := godog.TestSuite{Name: "Ordinary bots and Mini Apps", ScenarioInitializer: func(sc *godog.ScenarioContext) {
		var stand *stand
		var fileID, queryID, preparedID string
		var delivered chan map[string]any
		sc.Given(`^a stand with inline mode enabled$`, func() { stand = newStand(t, Options{Token: "123456:TOKEN", InlineMode: true}) })
		sc.When(`^the bot uploads a document containing notes$`, func() error {
			status, body := stand.upload("sendDocument", map[string]string{"chat_id": "4242"}, "document", "notes.txt", []byte("notes"))
			if status != 200 {
				return fmt.Errorf("upload: %d %v", status, body)
			}
			fileID = result(t, body)["document"].(map[string]any)["file_id"].(string)
			return nil
		})
		sc.Then(`^getFile gives a working Telegram download path$`, func() error {
			_, body := stand.call("getFile", url.Values{"file_id": {fileID}})
			path := result(t, body)["file_path"].(string)
			response, err := http.Get(stand.srv.URL + "/file/bot123456:TOKEN/" + path)
			if err != nil {
				return err
			}
			defer func() { _ = response.Body.Close() }()
			raw, _ := io.ReadAll(response.Body)
			if response.StatusCode != 200 || string(raw) != "notes" {
				return fmt.Errorf("download: %d %q", response.StatusCode, raw)
			}
			return nil
		})
		sc.Then(`^the bot can send the same file id again$`, func() error {
			status, body := stand.call("sendDocument", url.Values{"chat_id": {"4242"}, "document": {fileID}})
			if status != 200 {
				return fmt.Errorf("reuse: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the person asks the inline bot for hello$`, func() error {
			status, body := stand.sim("POST", "/sim/inline", map[string]any{"query": "hello"})
			if status != 200 {
				return fmt.Errorf("inline: %d %v", status, body)
			}
			queryID = body["inline_query_id"].(string)
			return nil
		})
		sc.When(`^the bot offers an article containing an inline answer$`, func() error {
			status, body := stand.call("answerInlineQuery", url.Values{"inline_query_id": {queryID}, "results": {"[" + article + "]"}})
			if status != 200 {
				return fmt.Errorf("answer: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the person chooses that article$`, func() error {
			status, body := stand.sim("POST", "/sim/inline/choose", map[string]any{"inline_query_id": queryID, "result_id": "one"})
			if status != 200 {
				return fmt.Errorf("choose: %d %v", status, body)
			}
			return nil
		})
		sc.Then(`^the chat shows the inline answer from the person$`, func() error {
			if !strings.Contains(stand.fake.Chat(4242).Text(), "alice: inline answer") {
				return fmt.Errorf("chat: %s", stand.fake.Chat(4242).Text())
			}
			return nil
		})
		sc.When(`^the bot prepares an article for the default Mini App user$`, func() error {
			status, body := stand.call("savePreparedInlineMessage", url.Values{"user_id": {"4242"}, "result": {article}, "allow_user_chats": {"true"}})
			if status != 200 {
				return fmt.Errorf("prepare: %d %v", status, body)
			}
			preparedID = result(t, body)["id"].(string)
			return nil
		})
		sc.When(`^the person shares it into a private chat$`, func() error {
			status, body := stand.sim("POST", "/sim/webapp/share", map[string]any{"id": preparedID})
			if status != 200 {
				return fmt.Errorf("share: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the bot registers a local webhook$`, func() error {
			delivered = make(chan map[string]any, 1)
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var update map[string]any
				_ = json.NewDecoder(req.Body).Decode(&update)
				delivered <- update
				w.WriteHeader(200)
			}))
			t.Cleanup(func() { stand.fake.Close(); receiver.Close() })
			status, body := stand.call("setWebhook", url.Values{"url": {receiver.URL}})
			if status != 200 {
				return fmt.Errorf("setWebhook: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the person sends a webhook message$`, func() { stand.fake.InjectMessage(IncomingMessage{Text: "webhook message"}) })
		sc.Then(`^the webhook receives the message update$`, func() error {
			select {
			case update := <-delivered:
				if update["message"].(map[string]any)["text"] != "webhook message" {
					return fmt.Errorf("update: %v", update)
				}
				return nil
			case <-time.After(time.Second):
				return fmt.Errorf("no webhook delivery")
			}
		})
		sc.When(`^a test configures a group with the default user as owner$`, func() error {
			status, body := stand.sim("POST", "/sim/chat/configure", map[string]any{"chat": map[string]any{"id": -100, "type": "supergroup", "title": "Group"}, "members": []map[string]any{{"status": "creator", "user": map[string]any{"id": 4242, "first_name": "Alice"}}}})
			if status != 200 {
				return fmt.Errorf("configure: %d %v", status, body)
			}
			return nil
		})
		sc.Then(`^getChatMember reports that user as the owner$`, func() error {
			_, body := stand.call("getChatMember", url.Values{"chat_id": {"-100"}, "user_id": {"4242"}})
			if result(t, body)["status"] != "creator" {
				return fmt.Errorf("member: %v", body)
			}
			return nil
		})
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/bot_capabilities.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("ordinary bot capabilities feature failed")
	}
}
