package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
	"github.com/cucumber/godog"
)

func TestBotCommandsFeature(t *testing.T) {
	const (
		chatScope   = `{"type":"chat","chat_id":4242}`
		otherScope  = `{"type":"chat","chat_id":4243}`
		adminScope  = `{"type":"chat_administrators","chat_id":-100}`
		defaultList = `[{"command":"start","description":"Start"}]`
		englishList = `[{"command":"go","description":"Go on"}]`
		russianList = `[{"command":"poyekhali","description":"Poyekhali"}]`
		adminList   = `[{"command":"ban","description":"Ban"}]`
	)
	suite := godog.TestSuite{
		Name: "Bot commands per scope and language",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var stand *stand
			sc.Given(`^an offline Telegram stand$`, func() {
				stand = newStand(t, Options{})
			})
			sc.When(`^a test configures a group with the default user as owner$`, func() error {
				status, body := stand.sim("POST", "/sim/chat/configure", map[string]any{
					"chat":    map[string]any{"id": -100, "type": "supergroup", "title": "Group"},
					"members": []map[string]any{{"status": "creator", "user": map[string]any{"id": 4242, "first_name": "Alice"}}},
				})
				if status != 200 {
					return fmt.Errorf("configure: %d %v", status, body)
				}
				return nil
			})
			sc.When(`^the bot registers a default command list$`, func() error {
				return setCommands(stand, "", "", defaultList)
			})
			sc.When(`^the bot registers an English command list for one private chat$`, func() error {
				return setCommands(stand, chatScope, "en", englishList)
			})
			sc.When(`^the bot registers a Russian list and an English list for one private chat$`, func() error {
				if err := setCommands(stand, chatScope, "ru", russianList); err != nil {
					return err
				}
				return setCommands(stand, chatScope, "en", englishList)
			})
			sc.When(`^the bot deletes that private chat's commands$`, func() error {
				status, body := stand.call("deleteMyCommands", url.Values{"scope": {chatScope}, "language_code": {"en"}})
				if status != 200 {
					return fmt.Errorf("deleteMyCommands: %d %v", status, body)
				}
				return nil
			})
			sc.When(`^the bot registers a command list for that group's administrators$`, func() error {
				return setCommands(stand, adminScope, "", adminList)
			})
			sc.Then(`^that private chat shows the English list$`, func() error {
				return expectCommands(stand, chatScope, "en", englishList)
			})
			sc.Then(`^another private chat shows the default list$`, func() error {
				return expectCommands(stand, otherScope, "en", defaultList)
			})
			sc.Then(`^that private chat in Russian shows the Russian list$`, func() error {
				return expectCommands(stand, chatScope, "ru", russianList)
			})
			sc.Then(`^that private chat in English shows the English list$`, func() error {
				return expectCommands(stand, chatScope, "en", englishList)
			})
			sc.Then(`^that private chat shows the default list$`, func() error {
				return expectCommands(stand, chatScope, "en", defaultList)
			})
			sc.Then(`^an administrator of that group shows that list$`, func() error {
				return expectCommands(stand, `{"type":"chat_member","chat_id":-100,"user_id":4242}`, "en", adminList)
			})
			sc.Then(`^a plain member of that group shows the default list$`, func() error {
				return expectCommands(stand, `{"type":"chat_member","chat_id":-100,"user_id":4243}`, "en", defaultList)
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/bot_commands.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("bot commands feature failed")
	}
}

func setCommands(stand *stand, scope, language, commands string) error {
	form := url.Values{"commands": {commands}}
	if scope != "" {
		form.Set("scope", scope)
	}
	if language != "" {
		form.Set("language_code", language)
	}
	status, body := stand.call("setMyCommands", form)
	if status != 200 {
		return fmt.Errorf("setMyCommands: %d %v", status, body)
	}
	return nil
}

// expectCommands asserts the answer of getMyCommands against the list it is
// meant to show, as the JSON the bot registered it with.
func expectCommands(stand *stand, scope, language, want string) error {
	form := url.Values{"scope": {scope}}
	if language != "" {
		form.Set("language_code", language)
	}
	status, body := stand.call("getMyCommands", form)
	if status != 200 {
		return fmt.Errorf("getMyCommands: %d %v", status, body)
	}
	raw, err := json.Marshal(body["result"])
	if err != nil {
		return fmt.Errorf("result: %w", err)
	}
	got := make([]botapi.BotCommand, 0, 4)
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("result %s: %w", raw, err)
	}
	wantList := make([]botapi.BotCommand, 0, 4)
	if err := json.Unmarshal([]byte(want), &wantList); err != nil {
		return fmt.Errorf("want %s: %w", want, err)
	}
	if len(got) != len(wantList) {
		return fmt.Errorf("commands for %s %q: %s, want %s", scope, language, raw, want)
	}
	for i := range got {
		if got[i] != wantList[i] {
			return fmt.Errorf("commands for %s %q: %s, want %s", scope, language, raw, want)
		}
	}
	return nil
}
