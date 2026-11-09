package server

import (
	"math/rand"
	"net/url"
	"strings"
	"testing"
)

func TestSendWidgetStoresCodeStateAndCaption(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.call("sendWidget", url.Values{
		"chat_id": {"4242"}, "html": {`<h3>Order</h3><button>Track</button>`},
		"width": {"320"}, "height": {"120"}, "public_data": {"public"},
		"chat_data": {"private"}, "caption": {"Order #42"}, "show_caption_above_media": {"true"},
		"allow_queries": {"true"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Again","callback_data":"again"}]]}`},
	})
	if status != 200 {
		t.Fatalf("sendWidget: %d %v", status, body)
	}
	message := result(t, body)
	widget := message["widget"].(map[string]any)
	if widget["html"] != `<h3>Order</h3><button>Track</button>` || widget["width"] != float64(320) || widget["height"] != float64(120) || widget["public_data"] != "public" || widget["chat_data"] != "private" {
		t.Fatalf("widget: %v", widget)
	}
	if _, shared := widget["id"]; shared {
		t.Fatalf("static widget has an id: %v", widget)
	}
	if message["show_caption_above_media"] != true || message["caption"] != "Order #42" {
		t.Fatalf("message: %v", message)
	}
	view := s.fake.Chat(4242).Messages[0]
	if view.Widget == nil || !view.WidgetAllowQueries || !strings.Contains(s.fake.Chat(4242).Text(), "[widget] Order #42") {
		t.Fatalf("view: %+v\n%s", view, s.fake.Chat(4242).Text())
	}
}

func TestSendWidgetValidationIsAtomic(t *testing.T) {
	s := newStand(t, Options{})
	random := rand.New(rand.NewSource(1))
	incompressible := make([]byte, 12000)
	for i := range incompressible {
		incompressible[i] = byte("abcdefghijklmnopqrstuvwxyz012345"[random.Intn(32)])
	}
	for _, tc := range []struct {
		name, html, field, value, description string
	}{
		{"empty html", "", "", "", "Bad Request: INPUT_MEDIA_WIDGET_HTML_INVALID"},
		{"NUL", "before\x00after", "", "", "Bad Request: INPUT_MEDIA_WIDGET_HTML_INVALID"},
		{"raw size", strings.Repeat("x", 32769), "", "", "Bad Request: INPUT_MEDIA_WIDGET_TOO_LARGE"},
		{"compressed size", string(incompressible), "", "", "Bad Request: INPUT_MEDIA_WIDGET_TOO_LARGE"},
		{"public data", "<p>x</p>", "public_data", strings.Repeat("x", 1025), "Bad Request: INPUT_MEDIA_WIDGET_DATA_INVALID"},
		{"chat data", "<p>x</p>", "chat_data", strings.Repeat("x", 1025), "Bad Request: INPUT_MEDIA_WIDGET_DATA_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"chat_id": {"4242"}, "html": {tc.html}}
			if tc.field != "" {
				form.Set(tc.field, tc.value)
			}
			status, body := s.call("sendWidget", form)
			if status != 400 || body["description"] != tc.description {
				t.Fatalf("sendWidget: %d %v", status, body)
			}
		})
	}
	status, body := s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>x</p>"}, "caption": {strings.Repeat("x", 1025)}})
	if status != 400 || body["description"] != "Bad Request: message caption is too long" {
		t.Fatalf("caption: %d %v", status, body)
	}
	if got := len(s.fake.Chat(4242).Messages); got != 0 {
		t.Fatalf("refused sends stored %d messages", got)
	}
}

func TestSharedWidgetStateReachesEveryMessage(t *testing.T) {
	s := newStand(t, Options{})
	_, first := s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>price</p>"}, "public_data": {"old"}, "chat_data": {"one"}, "is_shared": {"true"}, "allow_queries": {"true"}})
	id := result(t, first)["widget"].(map[string]any)["id"].(string)
	status, second := s.call("sendSharedWidget", url.Values{"chat_id": {"7"}, "widget_id": {id}, "chat_data": {"two"}})
	if status != 200 || result(t, second)["widget"].(map[string]any)["id"] != id {
		t.Fatalf("sendSharedWidget: %d %v", status, second)
	}
	status, body := s.call("editSharedWidgetData", url.Values{"widget_id": {id}, "public_data": {"new"}, "cache_time": {"10"}})
	if status != 200 || body["result"] != true {
		t.Fatalf("editSharedWidgetData: %d %v", status, body)
	}
	for chatID, chatData := range map[int64]string{4242: "one", 7: "two"} {
		widget := s.fake.Chat(chatID).Messages[0].Widget
		if widget.PublicData != "new" || widget.ChatData != chatData || widget.ID != id {
			t.Fatalf("chat %d: %+v", chatID, widget)
		}
	}
	status, body = s.call("editMessageWidgetData", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "public_data": {"message edit"}, "chat_data": {""}})
	if status != 200 || body["result"] != true {
		t.Fatalf("editMessageWidgetData: %d %v", status, body)
	}
	if got := s.fake.Chat(7).Messages[0].Widget.PublicData; got != "message edit" {
		t.Fatalf("second message kept public data %q", got)
	}
	if got := s.fake.Chat(4242).Messages[0].Widget.ChatData; got != "" {
		t.Fatalf("chat data was not cleared: %q", got)
	}
	status, body = s.call("editMessageWidgetData", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "public_data": {"message edit"}})
	if status != 400 || body["description"] != "Bad Request: message is not modified" {
		t.Fatalf("unchanged edit: %d %v", status, body)
	}
	status, body = s.call("sendSharedWidget", url.Values{"chat_id": {"7"}, "widget_id": {"missing"}})
	if status != 400 || body["description"] != "Bad Request: widget not found" {
		t.Fatalf("unknown widget: %d %v", status, body)
	}
}

func TestWidgetForwardDropsChatStateAndCopyIsRefused(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>game</p>"}, "public_data": {"score"}, "chat_data": {"secret"}, "allow_queries": {"true"}})
	status, body := s.call("copyMessage", url.Values{"chat_id": {"7"}, "from_chat_id": {"4242"}, "message_id": {"1"}})
	if status != 400 || body["description"] != "Bad Request: the message can't be copied" {
		t.Fatalf("copyMessage: %d %v", status, body)
	}
	status, body = s.call("forwardMessage", url.Values{"chat_id": {"7"}, "from_chat_id": {"4242"}, "message_id": {"1"}})
	if status != 200 {
		t.Fatalf("forwardMessage: %d %v", status, body)
	}
	widget := result(t, body)["widget"].(map[string]any)
	if widget["public_data"] != "score" {
		t.Fatalf("forward public data: %v", widget)
	}
	if _, present := widget["chat_data"]; present {
		t.Fatalf("forward carried chat data: %v", widget)
	}
	if _, _, err := s.fake.InjectWidgetQuery(IncomingWidgetQuery{ChatID: 7, MessageID: 1, Data: "move"}); err == nil {
		t.Fatal("a static forward sent a query")
	}
}

func TestWidgetQueryAnswerAndForwardedSharedQuery(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<button>Ask</button>"}, "is_shared": {"true"}, "allow_queries": {"true"}})
	updateID, queryID, err := s.fake.InjectWidgetQuery(IncomingWidgetQuery{Data: "refresh"})
	if err != nil || queryID != "wq-1" {
		t.Fatalf("InjectWidgetQuery: %d %q %v", updateID, queryID, err)
	}
	_, body := s.call("getUpdates", url.Values{"offset": {itoa(updateID)}})
	batch := updates(t, body)
	if len(batch) != 1 || batch[0]["widget_query"] == nil {
		t.Fatalf("updates: %v", batch)
	}
	query := batch[0]["widget_query"].(map[string]any)
	if query["id"] != queryID || query["data"] != "refresh" || query["chat_instance"] != "4242" || query["type"].(map[string]any)["type"] != "data" || query["widget_id"] == "" {
		t.Fatalf("widget query: %v", query)
	}
	status, body := s.call("answerWidgetQuery", url.Values{"widget_query_id": {queryID}, "data": {"fresh"}})
	if status != 200 || body["result"] != true {
		t.Fatalf("answerWidgetQuery: %d %v", status, body)
	}
	data, answered, ok := s.fake.WidgetAnswer(queryID)
	if !ok || !answered || data != "fresh" {
		t.Fatalf("WidgetAnswer: %q %v %v", data, answered, ok)
	}
	status, body = s.call("answerWidgetQuery", url.Values{"widget_query_id": {queryID}})
	if status != 400 || body["description"] != queryInvalid {
		t.Fatalf("second answer: %d %v", status, body)
	}
	status, body = s.call("answerWidgetQuery", url.Values{"widget_query_id": {"unknown"}})
	if status != 400 || body["description"] != queryInvalid {
		t.Fatalf("unknown answer: %d %v", status, body)
	}

	s.call("forwardMessage", url.Values{"chat_id": {"7"}, "from_chat_id": {"4242"}, "message_id": {"1"}})
	_, _, err = s.fake.InjectWidgetQuery(IncomingWidgetQuery{ChatID: 7, Data: "forward request"})
	if err != nil {
		t.Fatalf("shared forward query: %v", err)
	}
	_, body = s.call("getUpdates", url.Values{"offset": {itoa(updateID + 1)}})
	batch = updates(t, body)
	if len(batch) != 1 || batch[0]["shared_widget_query"] == nil {
		t.Fatalf("forward updates: %v", batch)
	}
	forward := batch[0]["shared_widget_query"].(map[string]any)
	if forward["data"] != "forward request" || forward["widget_id"] == "" || forward["chat"] != nil || forward["message_id"] != nil {
		t.Fatalf("shared widget query: %v", forward)
	}
}

func TestWidgetQueryRequiresPermissionAndValidData(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>read only</p>"}})
	for _, data := range []string{"", strings.Repeat("x", 65), "request"} {
		if _, _, err := s.fake.InjectWidgetQuery(IncomingWidgetQuery{Data: data}); err == nil {
			t.Fatalf("query %q was accepted", data)
		}
	}
	if got := s.fake.PendingUpdates(); got != 0 {
		t.Fatalf("refused queries queued %d updates", got)
	}
}

func TestWidgetDataEditValidationAndReset(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"not a widget"}})
	status, body := s.call("editMessageWidgetData", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "chat_data": {"x"}})
	if status != 400 || body["description"] != "Bad Request: message can't be edited" {
		t.Fatalf("ordinary message edit: %d %v", status, body)
	}
	_, sent := s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>shared</p>"}, "public_data": {"same"}, "is_shared": {"true"}, "allow_queries": {"true"}})
	id := result(t, sent)["widget"].(map[string]any)["id"].(string)
	for _, tc := range []struct {
		name, description string
		form              url.Values
	}{
		{"required public data", `Bad Request: parameter "public_data" is required`, url.Values{"widget_id": {id}}},
		{"cache floor", "Bad Request: CACHE_TIME_INVALID", url.Values{"widget_id": {id}, "public_data": {"same"}, "cache_time": {"4"}}},
		{"unchanged", "Bad Request: message is not modified", url.Values{"widget_id": {id}, "public_data": {"same"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := s.call("editSharedWidgetData", tc.form)
			if status != 400 || body["description"] != tc.description {
				t.Fatalf("editSharedWidgetData: %d %v", status, body)
			}
		})
	}
	status, body = s.call("editSharedWidgetData", url.Values{"widget_id": {id}, "public_data": {""}, "cache_time": {"9999"}})
	if status != 200 || s.fake.sharedWidgets[id].cacheTime != 3600 || s.fake.Chat(4242).Messages[1].Widget.PublicData != "" {
		t.Fatalf("cache clamp and clear: %d %v %+v", status, body, s.fake.sharedWidgets[id])
	}

	_, queryID, err := s.fake.InjectWidgetQuery(IncomingWidgetQuery{MessageID: 2, Data: "request"})
	if err != nil {
		t.Fatal(err)
	}
	status, body = s.call("answerWidgetQuery", url.Values{"widget_query_id": {queryID}, "data": {strings.Repeat("x", 1025)}})
	if status != 400 || body["description"] != "Bad Request: data is too long" {
		t.Fatalf("long answer: %d %v", status, body)
	}
	if _, answered, ok := s.fake.WidgetAnswer(queryID); !ok || answered {
		t.Fatalf("refused answer consumed the query: %v %v", answered, ok)
	}
	s.fake.Reset()
	if _, _, ok := s.fake.WidgetAnswer(queryID); ok {
		t.Fatal("reset kept the widget query")
	}
	status, _ = s.call("sendSharedWidget", url.Values{"chat_id": {"4242"}, "widget_id": {id}})
	if status != 400 {
		t.Fatalf("reset kept shared widget: %d", status)
	}
	_, next := s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>new</p>"}, "is_shared": {"true"}})
	if got := result(t, next)["widget"].(map[string]any)["id"]; got != "widget-2" {
		t.Fatalf("shared id was reused after reset: %v", got)
	}
}

func TestWidgetCaptionCanChangeButMediaCannotReplaceIt(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<p>fixed</p>"}, "caption": {"old"}})
	status, body := s.call("editMessageCaption", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "caption": {"new"}})
	if status != 200 || result(t, body)["caption"] != "new" || result(t, body)["widget"] == nil {
		t.Fatalf("editMessageCaption: %d %v", status, body)
	}
	status, body = s.upload("editMessageMedia", map[string]string{
		"chat_id": "4242", "message_id": "1", "media": `{"type":"photo","media":"attach://upload"}`,
	}, "upload", "a.png", mediaPNG(t, 2, 2))
	if status != 400 || body["description"] != "Bad Request: message can't be edited" {
		t.Fatalf("editMessageMedia: %d %v", status, body)
	}
	message := s.fake.Chat(4242).Messages[0]
	if message.Widget == nil || message.Widget.HTML != "<p>fixed</p>" || message.Caption != "new" || message.Photo != nil {
		t.Fatalf("refused replacement changed the message: %+v", message)
	}
}

func TestWidgetQueryHonoursAllowedUpdates(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendWidget", url.Values{"chat_id": {"4242"}, "html": {"<button>Ask</button>"}, "allow_queries": {"true"}})
	s.fake.SetAllowedUpdates([]string{"message"})
	if _, _, err := s.fake.InjectWidgetQuery(IncomingWidgetQuery{Data: "excluded"}); err != nil {
		t.Fatal(err)
	}
	if got := s.fake.PendingUpdates(); got != 0 {
		t.Fatalf("excluded widget query was queued: %d", got)
	}
	s.fake.SetAllowedUpdates([]string{"widget_query"})
	if _, _, err := s.fake.InjectWidgetQuery(IncomingWidgetQuery{Data: "included"}); err != nil {
		t.Fatal(err)
	}
	_, body := s.call("getUpdates", nil)
	batch := updates(t, body)
	if len(batch) != 1 || batch[0]["widget_query"] == nil || batch[0]["widget_query"].(map[string]any)["data"] != "included" {
		t.Fatalf("filtered updates: %v", batch)
	}
}
