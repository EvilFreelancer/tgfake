package server

import (
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestFakeLifecycleFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "Telegram stand lifetimes",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var s *stand
			var elapsed atomic.Int64
			var batch []map[string]any
			sc.Given(`^an offline Telegram stand$`, func() {
				s = newStand(t, Options{})
				start := time.Now()
				s.fake.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
			})
			sc.Given(`^a user message is queued$`, func() {
				s.fake.InjectMessage(IncomingMessage{Text: "queued"})
			})
			sc.When(`^the bot subscribes to callback queries only$`, func() {
				_, body := s.call("getUpdates", url.Values{"allowed_updates": {`["callback_query"]`}})
				batch = updates(t, body)
			})
			sc.Then(`^the queued message is delivered$`, func() error {
				if len(batch) != 1 || batch[0]["message"] == nil {
					return fmt.Errorf("queued message missing: %v", batch)
				}
				return nil
			})
			sc.When(`^the bot streams a rich preview$`, func() {
				_, body := s.call("sendRichMessageDraft", url.Values{
					"chat_id": {"4242"}, "draft_id": {"1"}, "rich_message": {`{"markdown":"partial"}`},
				})
				if body["ok"] != true {
					t.Fatalf("draft rejected: %v", body)
				}
			})
			sc.When(`^the bot streams a rich preview with a Stop button$`, func() error {
				_, body := s.call("sendRichMessageDraft", url.Values{
					"chat_id":      {"4242"},
					"draft_id":     {"-7"},
					"can_stop":     {"true"},
					"rich_message": {`{"markdown":"partial"}`},
				})
				if body["ok"] != true {
					return fmt.Errorf("draft rejected: %v", body)
				}
				return nil
			})
			sc.When(`^the person presses Stop under it$`, func() error {
				status, body := s.sim("POST", "/sim/draft/stop", map[string]any{})
				if status != http.StatusOK {
					return fmt.Errorf("stop refused: %d %v", status, body)
				}
				return nil
			})
			sc.Then(`^the bot's next poll carries the stopped draft$`, func() error {
				_, body := s.call("getUpdates", nil)
				batch := updates(t, body)
				if len(batch) != 1 {
					return fmt.Errorf("poll carried %v", batch)
				}

				stop, _ := batch[0]["stopped_message_generation"].(map[string]any)
				chat, _ := stop["chat"].(map[string]any)
				if stop["draft_id"] != float64(-7) || chat["id"] != float64(4242) || chat["type"] != "private" {
					return fmt.Errorf("poll carried %v", batch)
				}
				return nil
			})
			sc.When(`^(\d+) seconds pass without a new revision$`, func(seconds int) {
				elapsed.Add(int64(time.Duration(seconds) * time.Second))
			})
			sc.Then(`^the chat shows (\d+) rich previews?$`, func(want int) error {
				_, body := s.sim("GET", "/sim/chat/4242", nil)
				if got := len(body["drafts"].([]any)); got != want {
					return fmt.Errorf("chat shows %d rich previews, want %d", got, want)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/update_lifecycle.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Telegram stand lifecycle feature failed")
	}
}
