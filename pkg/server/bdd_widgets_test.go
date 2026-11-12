package server

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

func TestWidgetsFeature(t *testing.T) {
	suite := godog.TestSuite{Name: "HTML widgets", ScenarioInitializer: func(sc *godog.ScenarioContext) {
		var stand *stand
		var queryID, widgetID string
		sc.Given(`^an offline Telegram stand$`, func() { stand = newStand(t, Options{}) })
		sc.When(`^the bot sends a widget with an initial counter$`, func() error {
			status, body := stand.call("sendWidget", url.Values{
				"chat_id": {"4242"}, "html": {`<button onclick="TelegramWidget.request('increment')">Increment</button>`},
				"caption": {"Counter"}, "chat_data": {"0"}, "allow_queries": {"true"},
			})
			if status != 200 {
				return fmt.Errorf("sendWidget: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the person asks the widget to increment the counter$`, func() error {
			status, body := stand.sim("POST", "/sim/widget/query", map[string]any{"data": "increment"})
			if status != 200 {
				return fmt.Errorf("widget query: %d %v", status, body)
			}
			queryID = body["widget_query_id"].(string)
			return nil
		})
		sc.Then(`^the bot's next poll carries the widget query$`, func() error {
			_, body := stand.call("getUpdates", nil)
			batch := updates(t, body)
			if len(batch) != 1 || batch[0]["widget_query"] == nil {
				return fmt.Errorf("updates: %v", batch)
			}
			query := batch[0]["widget_query"].(map[string]any)
			if query["id"] != queryID || query["data"] != "increment" || query["message_id"] != float64(1) {
				return fmt.Errorf("widget query: %v", query)
			}
			return nil
		})
		sc.When(`^the bot answers the widget query with the new counter$`, func() error {
			status, body := stand.call("answerWidgetQuery", url.Values{"widget_query_id": {queryID}, "data": {"1"}})
			if status != 200 {
				return fmt.Errorf("answerWidgetQuery: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the bot changes the widget counter to 1$`, func() error {
			status, body := stand.call("editMessageWidgetData", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "chat_data": {"1"}})
			if status != 200 {
				return fmt.Errorf("editMessageWidgetData: %d %v", status, body)
			}
			return nil
		})
		sc.Then(`^the chat shows the widget counter 1$`, func() error {
			messages := stand.fake.Chat(4242).Messages
			if len(messages) != 1 || messages[0].Widget == nil || messages[0].Widget.ChatData != "1" {
				return fmt.Errorf("messages: %+v", messages)
			}
			return nil
		})
		sc.When(`^the bot sends a shared widget with a public price$`, func() error {
			status, body := stand.call("sendWidget", url.Values{
				"chat_id": {"4242"}, "html": {`<p>TON price</p>`}, "public_data": {"5.40"},
				"chat_data": {"first"}, "is_shared": {"true"},
			})
			if status != 200 {
				return fmt.Errorf("sendWidget: %d %v", status, body)
			}
			widgetID = result(t, body)["widget"].(map[string]any)["id"].(string)
			return nil
		})
		sc.When(`^the bot sends the same shared widget to another chat$`, func() error {
			status, body := stand.call("sendSharedWidget", url.Values{"chat_id": {"7"}, "widget_id": {widgetID}, "chat_data": {"second"}})
			if status != 200 {
				return fmt.Errorf("sendSharedWidget: %d %v", status, body)
			}
			return nil
		})
		sc.When(`^the bot changes the shared price to 5.47$`, func() error {
			status, body := stand.call("editSharedWidgetData", url.Values{"widget_id": {widgetID}, "public_data": {"5.47"}, "cache_time": {"10"}})
			if status != 200 {
				return fmt.Errorf("editSharedWidgetData: %d %v", status, body)
			}
			return nil
		})
		sc.Then(`^both chats show the shared price 5.47$`, func() error {
			for _, chatID := range []int64{4242, 7} {
				messages := stand.fake.Chat(chatID).Messages
				if len(messages) != 1 || messages[0].Widget == nil || messages[0].Widget.PublicData != "5.47" || messages[0].Widget.ID != widgetID {
					return fmt.Errorf("chat %d: %+v", chatID, messages)
				}
			}
			return nil
		})
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/widgets.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("widgets feature failed")
	}
}
