package main

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

func TestCommandFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "tgfake command",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var c *command
			var me map[string]any
			var answer string
			sc.Given(`^the tgfake command is serving with its scripted model$`, func() {
				c = startCommand(t, "--llm", "--llm-delay", "0s")
			})
			sc.When(`^a bot asks the Bot API who it is$`, func() {
				me = c.botCall("getMe", nil)
			})
			sc.Then(`^it learns it is @(\S+)$`, func(username string) error {
				result, _ := me["result"].(map[string]any)
				if me["ok"] != true || result["username"] != username || result["is_bot"] != true {
					return fmt.Errorf("getMe answered %v, want the bot @%s", me, username)
				}
				return nil
			})
			sc.When(`^the person sends "([^"]*)" through the simulation API$`, func(text string) error {
				if body := c.postJSON("/sim/message", map[string]any{"text": text}); body["update_id"] == nil {
					return fmt.Errorf("/sim/message answered %v", body)
				}
				return nil
			})
			sc.Then(`^the bot's next getUpdates carries the message "([^"]*)"$`, func(text string) error {
				body := c.botCall("getUpdates", url.Values{"timeout": {"0"}})
				updates, _ := body["result"].([]any)
				if len(updates) != 1 {
					return fmt.Errorf("getUpdates answered %v, want one update", body)
				}
				msg, _ := updates[0].(map[string]any)["message"].(map[string]any)
				if msg["text"] != text {
					return fmt.Errorf("the update carries %v, want the message %q", updates[0], text)
				}
				return nil
			})
			sc.When(`^the bot asks the model to answer "([^"]*)"$`, func(text string) {
				answer = c.completion(map[string]any{"role": "user", "content": text})
			})
			sc.Then(`^the model answers "([^"]*)"$`, func(want string) error {
				if answer != want {
					return fmt.Errorf("the model answered %q, want %q", answer, want)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/command.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("tgfake command feature failed")
	}
}
