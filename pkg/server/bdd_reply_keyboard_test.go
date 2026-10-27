package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func TestReplyKeyboardFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "Reply keyboards and ForceReply",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var stand *stand
			var offset, requestID int
			send := func(markup any) (map[string]any, error) {
				raw, _ := json.Marshal(markup)
				status, body := stand.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {string(raw)}})
				if status != 200 {
					return nil, fmt.Errorf("sendMessage: %d %v", status, body)
				}
				return result(t, body), nil
			}
			keyboard := func(labels string, oneTime bool) error {
				var row []map[string]string
				for _, label := range strings.Split(labels, ", ") {
					row = append(row, map[string]string{"text": label})
				}
				_, err := send(map[string]any{"keyboard": [][]map[string]string{row}, "one_time_keyboard": oneTime})
				return err
			}
			// receive takes the updates since the last confirmed one.
			receive := func() []map[string]any {
				_, body := stand.call("getUpdates", url.Values{"offset": {strconv.Itoa(offset)}})
				batch := updates(t, body)
				if len(batch) > 0 {
					offset = int(batch[len(batch)-1]["update_id"].(float64)) + 1
				}
				return batch
			}
			lastMessage := func(text string) (map[string]any, error) {
				batch := receive()
				if len(batch) != 1 || batch[0]["callback_query"] != nil {
					return nil, fmt.Errorf("want one message update, got %v", batch)
				}
				msg, _ := batch[0]["message"].(map[string]any)
				if msg == nil || msg["text"] != text || msg["from"].(map[string]any)["id"] != float64(4242) {
					return nil, fmt.Errorf("want %q from the person, got %v", text, batch[0])
				}
				return msg, nil
			}
			sc.Given(`^an offline Telegram stand$`, func() {
				stand, offset, requestID = newStand(t, Options{}), 0, 0
			})
			sc.Given(`^the bot sent a reply keyboard "([^"]*)"$`, func(labels string) error { return keyboard(labels, false) })
			sc.Given(`^the bot sent a one-time reply keyboard "([^"]*)"$`, func(labels string) error { return keyboard(labels, true) })
			sc.When(`^the bot sends a message with an inline keyboard$`, func() error {
				_, err := send(map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "Model", "callback_data": "model"}}}})
				return err
			})
			sc.When(`^the bot asks for a reply with ForceReply$`, func() error {
				msg, err := send(map[string]any{"force_reply": true, "input_field_placeholder": "Describe changes"})
				if err == nil {
					requestID = int(msg["message_id"].(float64))
				}
				return err
			})
			sc.When(`^the bot removes the reply keyboard$`, func() error {
				_, err := send(map[string]any{"remove_keyboard": true})
				return err
			})
			sc.When(`^the person presses "([^"]*)"$`, func(label string) error {
				menu := stand.fake.Chat(4242).ReplyKeyboard
				if menu == nil {
					return fmt.Errorf("the chat has no reply keyboard")
				}
				for r, row := range menu.Keyboard {
					for c, button := range row {
						if button.Text == label {
							status, body := stand.sim("POST", "/sim/keyboard/press", map[string]any{"row": r, "column": c})
							if status != 200 {
								return fmt.Errorf("press: %d %v", status, body)
							}
							return nil
						}
					}
				}
				return fmt.Errorf("no button %q in %v", label, menu.Keyboard)
			})
			sc.Then(`^the bot receives the message "([^"]*)" from the person$`, func(text string) error {
				_, err := lastMessage(text)
				return err
			})
			sc.Then(`^the bot receives the message "([^"]*)" as a reply to that request$`, func(text string) error {
				msg, err := lastMessage(text)
				if err != nil {
					return err
				}
				if quoted, _ := msg["reply_to_message"].(map[string]any); quoted == nil || quoted["message_id"] != float64(requestID) {
					return fmt.Errorf("want a reply to #%d, got %v", requestID, msg)
				}
				return nil
			})
			sc.Then(`^the chat shows the reply keyboard$`, func() error {
				if v := stand.fake.Chat(4242); v.ReplyKeyboard == nil || v.KeyboardHidden {
					return fmt.Errorf("want a shown reply keyboard, got %+v hidden %v", v.ReplyKeyboard, v.KeyboardHidden)
				}
				return nil
			})
			sc.Then(`^the chat shows the reply keyboard hidden$`, func() error {
				if v := stand.fake.Chat(4242); v.ReplyKeyboard == nil || !v.KeyboardHidden {
					return fmt.Errorf("want a hidden reply keyboard, got %+v hidden %v", v.ReplyKeyboard, v.KeyboardHidden)
				}
				return nil
			})
			sc.Then(`^the chat no longer waits for a reply$`, func() error {
				if force := stand.fake.Chat(4242).ForceReply; force != nil {
					return fmt.Errorf("ForceReply still outstanding: %+v", force)
				}
				return nil
			})
			sc.Then(`^a press of the first button is refused$`, func() error {
				if status, body := stand.sim("POST", "/sim/keyboard/press", map[string]any{"row": 0, "column": 0}); status != 404 {
					return fmt.Errorf("want 404, got %d %v", status, body)
				}
				return nil
			})
			sc.Then(`^the bot receives no update$`, func() error {
				if batch := receive(); len(batch) != 0 {
					return fmt.Errorf("want no update, got %v", batch)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/reply_keyboard.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("reply keyboard feature failed")
	}
}
