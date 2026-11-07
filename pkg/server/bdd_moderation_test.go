package server

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
	"github.com/cucumber/godog"
)

func TestModerationFeature(t *testing.T) {
	var s *stand
	var lastStatus int
	var lastBody map[string]any

	member := func(userID int64) map[string]any {
		code, body := s.call("getChatMember", url.Values{"chat_id": {"-100"}, "user_id": {fmt.Sprint(userID)}})
		if code != http.StatusOK {
			t.Fatalf("getChatMember: %d %v", code, body)
		}
		return result(t, body)
	}
	message := func(userID int64) {
		lastStatus, lastBody = s.sim("POST", "/sim/message", map[string]any{
			"chat_id": -100, "chat_type": "supergroup", "user_id": userID, "text": "hi",
		})
	}

	suite := godog.TestSuite{
		Name: "Chat moderation",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Given(`^a supergroup the bot administers$`, func() {
				s = newStand(t, Options{BotID: 7001})
				if err := s.fake.ConfigureChat(ChatConfiguration{
					Chat: botapi.ChatFullInfo{"id": int64(-100), "type": "supergroup", "title": "mods"},
					Members: []botapi.ChatMember{
						{"status": "administrator", "user": map[string]any{"id": int64(7001), "is_bot": true, "first_name": "tgfake"}, "can_restrict_members": true, "can_promote_members": true},
						{"status": "member", "user": map[string]any{"id": int64(42), "first_name": "Bob"}},
					},
				}); err != nil {
					t.Fatal(err)
				}
			})
			sc.When(`^the bot restricts member (\d+) from sending messages for an hour$`, func(userID int64) {
				until := time.Now().Add(time.Hour).Unix()
				lastStatus, lastBody = s.call("restrictChatMember", url.Values{
					"chat_id":     {"-100"},
					"user_id":     {fmt.Sprint(userID)},
					"permissions": {`{"can_send_messages":false}`},
					"until_date":  {fmt.Sprint(until)},
				})
				if lastStatus != http.StatusOK {
					t.Fatalf("restrict: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^the bot bans member (\d+)$`, func(userID int64) {
				lastStatus, lastBody = s.call("banChatMember", url.Values{"chat_id": {"-100"}, "user_id": {fmt.Sprint(userID)}})
				if lastStatus != http.StatusOK {
					t.Fatalf("ban: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^the bot unbans member (\d+)$`, func(userID int64) {
				lastStatus, lastBody = s.call("unbanChatMember", url.Values{"chat_id": {"-100"}, "user_id": {fmt.Sprint(userID)}})
				if lastStatus != http.StatusOK {
					t.Fatalf("unban: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^the bot promotes member (\d+) with the right to delete messages$`, func(userID int64) {
				lastStatus, lastBody = s.call("promoteChatMember", url.Values{
					"chat_id": {"-100"}, "user_id": {fmt.Sprint(userID)}, "can_delete_messages": {"true"},
				})
				if lastStatus != http.StatusOK {
					t.Fatalf("promote: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^user (\d+) asks to join the chat$`, func(userID int64) {
				lastStatus, lastBody = s.sim("POST", "/sim/join-request", map[string]any{
					"chat_id": -100, "user": map[string]any{"id": userID, "first_name": "Carol"},
				})
				if lastStatus != http.StatusOK {
					t.Fatalf("join request: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^user (\d+) joins the chat$`, func(userID int64) {
				lastStatus, lastBody = s.sim("POST", "/sim/member", map[string]any{
					"chat_id": -100, "action": "join", "user": map[string]any{"id": userID, "first_name": "Dave"},
				})
				if lastStatus != http.StatusOK {
					t.Fatalf("join: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^the bot approves the join request of user (\d+)$`, func(userID int64) {
				lastStatus, lastBody = s.call("approveChatJoinRequest", url.Values{"chat_id": {"-100"}, "user_id": {fmt.Sprint(userID)}})
				if lastStatus != http.StatusOK {
					t.Fatalf("approve: %d %v", lastStatus, lastBody)
				}
			})
			sc.When(`^the stand's clock advances (\d+) hours$`, func(hours int64) {
				lastStatus, lastBody = s.sim("POST", "/sim/clock/advance", map[string]any{"seconds": hours * 3600})
				if lastStatus != http.StatusOK {
					t.Fatalf("clock: %d %v", lastStatus, lastBody)
				}
			})
			sc.Then(`^member (\d+) has the status "([^"]+)"$`, func(userID int64, want string) error {
				if got := member(userID)["status"]; got != want {
					return fmt.Errorf("member %d status = %v, want %q", userID, got, want)
				}
				return nil
			})
			sc.Then(`^member (\d+) is an administrator with "([^"]+)"$`, func(userID int64, right string) error {
				got := member(userID)
				if got["status"] != "administrator" || got[right] != true {
					return fmt.Errorf("member %d = %#v, want administrator with %s", userID, got, right)
				}
				return nil
			})
			sc.Then(`^a message from member (\d+) is refused$`, func(userID int64) error {
				message(userID)
				if lastStatus != http.StatusForbidden {
					return fmt.Errorf("message from %d: %d %v, want 403", userID, lastStatus, lastBody)
				}
				return nil
			})
			sc.Then(`^a message from member (\d+) is delivered$`, func(userID int64) error {
				message(userID)
				if lastStatus != http.StatusOK {
					return fmt.Errorf("message from %d: %d %v, want 200", userID, lastStatus, lastBody)
				}
				return nil
			})
			sc.Then(`^the bot's next poll carries the join request$`, func() error {
				_, body := s.call("getUpdates", nil)
				batch := updates(t, body)
				if len(batch) != 1 || batch[0]["chat_join_request"] == nil {
					return fmt.Errorf("poll carried %v", batch)
				}
				return nil
			})
			sc.Then(`^the bot's next poll carries the service message and the member update$`, func() error {
				_, body := s.call("getUpdates", nil)
				batch := updates(t, body)
				if len(batch) != 2 {
					return fmt.Errorf("poll carried %v", batch)
				}
				msg, _ := batch[0]["message"].(map[string]any)
				if msg == nil || msg["new_chat_members"] == nil {
					return fmt.Errorf("first update is not the service message: %v", batch[0])
				}
				if batch[1]["chat_member"] == nil {
					return fmt.Errorf("second update is not chat_member: %v", batch[1])
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/moderation.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("moderation feature failed")
	}
}
