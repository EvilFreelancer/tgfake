package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

func TestTransportFaultsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "Chat-scoped and download faults",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var stand *stand
			var filePath string
			// A fresh connection per request, so a dropped one is not reused.
			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
			fault := func(f map[string]any) error {
				if status, body := stand.sim("POST", "/sim/fault", f); status != 200 {
					return fmt.Errorf("fault: %d %v", status, body)
				}
				return nil
			}
			sendTo := func(chat string) (int, error) {
				resp, err := client.PostForm(stand.srv.URL+"/bot123456:TOKEN/sendMessage", url.Values{"chat_id": {chat}, "text": {"hello"}})
				if err != nil {
					return 0, err
				}
				_ = resp.Body.Close()
				return resp.StatusCode, nil
			}
			download := func() (string, error) {
				resp, err := client.Get(stand.srv.URL + "/file/bot123456:TOKEN/" + filePath)
				if err != nil {
					return "", err
				}
				defer func() { _ = resp.Body.Close() }()
				raw, err := io.ReadAll(resp.Body)
				return string(raw), err
			}
			sc.Given(`^an offline Telegram stand$`, func() { stand = newStand(t, Options{Token: "123456:TOKEN"}) })
			sc.Given(`^the bot has sent a document$`, func() error {
				status, body := stand.upload("sendDocument", map[string]string{"chat_id": "4242"}, "document", "notes.txt", []byte("notes"))
				if status != 200 {
					return fmt.Errorf("upload: %d %v", status, body)
				}
				fileID := result(t, body)["document"].(map[string]any)["file_id"].(string)
				_, body = stand.call("getFile", url.Values{"file_id": {fileID}})
				filePath = result(t, body)["file_path"].(string)
				return nil
			})
			sc.When(`^a test makes sendMessage to chat (\d+) fail with (\d+)$`, func(chat, code int) error {
				return fault(map[string]any{"method": "sendMessage", "chat_id": chat, "code": code})
			})
			sc.When(`^a test makes the next sendMessage to chat (\d+) lose its response after it applies$`, func(chat int) error {
				return fault(map[string]any{"method": "sendMessage", "chat_id": chat, "disconnect": true, "after_apply": true, "times": 1})
			})
			sc.When(`^a test makes the next file download disconnect$`, func() error {
				return fault(map[string]any{"target": "file", "disconnect": true, "times": 1})
			})
			sc.Then(`^the bot's message to chat (\d+) is sent$`, func(chat string) error {
				if status, err := sendTo(chat); err != nil || status != 200 {
					return fmt.Errorf("want 200, got %d %v", status, err)
				}
				return nil
			})
			sc.Then(`^the bot's message to chat (\d+) fails with (\d+)$`, func(chat string, code int) error {
				if status, err := sendTo(chat); err != nil || status != code {
					return fmt.Errorf("want %d, got %d %v", code, status, err)
				}
				return nil
			})
			sc.Then(`^the bot's message to chat (\d+) gets no response$`, func(chat string) error {
				if status, err := sendTo(chat); err == nil {
					return fmt.Errorf("want a network error, got %d", status)
				}
				return nil
			})
			sc.Then(`^chat (\d+) shows the message$`, func(chat int64) error {
				if messages := stand.fake.Chat(chat).Messages; len(messages) != 1 || messages[0].Text != "hello" {
					return fmt.Errorf("want the message in the chat, got %+v", messages)
				}
				return nil
			})
			sc.Then(`^the outbox marks the call applied with no response sent$`, func() error {
				calls := stand.fake.Calls("sendMessage")
				if len(calls) != 1 || calls[0].Fault == nil || !calls[0].Applied || calls[0].ResponseSent || calls[0].Status != 0 {
					return fmt.Errorf("want one applied call without a response, got %+v", calls)
				}
				return nil
			})
			sc.Then(`^the bot's download of the document fails$`, func() error {
				if data, err := download(); err == nil {
					return fmt.Errorf("want a network error, got %q", data)
				}
				return nil
			})
			sc.Then(`^the bot's next download of the document succeeds$`, func() error {
				if data, err := download(); err != nil || data != "notes" {
					return fmt.Errorf("want the document, got %q %v", data, err)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/transport_faults.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("transport faults feature failed")
	}
}
