package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// starsInvoiceForm is a Stars invoice every refusal case starts from.
func starsInvoiceForm() url.Values {
	return url.Values{
		"chat_id":     {"4242"},
		"title":       {"Premium"},
		"description": {"One month"},
		"payload":     {"order-7"},
		"currency":    {"XTR"},
		"prices":      {`[{"label":"Premium","amount":150}]`},
	}
}

func TestSendInvoice_Refusals(t *testing.T) {
	s := newStand(t, Options{})
	cases := []struct {
		name   string
		mutate func(url.Values)
		want   string
	}{
		{"no chat", func(f url.Values) { f.Del("chat_id") }, "Bad Request: chat_id is empty"},
		{"empty title", func(f url.Values) { f.Set("title", "") }, `Bad Request: Parameter "title" is required`},
		{"title over 32 characters", func(f url.Values) { f.Set("title", strings.Repeat("т", 33)) }, "Bad Request: title is too long"},
		{"empty description", func(f url.Values) { f.Set("description", "") }, `Bad Request: Parameter "description" is required`},
		{"description over 255 characters", func(f url.Values) { f.Set("description", strings.Repeat("a", 256)) }, "Bad Request: description is too long"},
		{"empty payload", func(f url.Values) { f.Set("payload", "") }, `Bad Request: Parameter "payload" is required`},
		{"a payload that is not UTF-8", func(f url.Values) { f.Set("payload", "\xff") }, "Bad Request: The payload must be encoded in UTF-8"},
		{"payload over 128 bytes", func(f url.Values) { f.Set("payload", strings.Repeat("я", 65)) }, "Bad Request: payload is too long"},
		{"empty currency", func(f url.Values) { f.Set("currency", "") }, `Bad Request: Parameter "currency" is required`},
		{"no prices", func(f url.Values) { f.Del("prices") }, `Bad Request: Parameter "prices" is required`},
		{"prices that are not JSON", func(f url.Values) { f.Set("prices", `[{amount:1}]`) }, "Bad Request: Can't parse prices JSON object"},
		{"prices not a list", func(f url.Values) { f.Set("prices", `{"amount":1}`) }, "Bad Request: Expected an Array of LabeledPrice"},
		{"a price that is not an object", func(f url.Values) { f.Set("prices", `[150]`) }, "Bad Request: Can't parse LabeledPrice: LabeledPrice must be an Object"},
		{"a price with no label", func(f url.Values) { f.Set("prices", `[{"amount":150}]`) }, `Bad Request: Can't parse LabeledPrice: Can't find field "label"`},
		{"a price with an empty label", func(f url.Values) { f.Set("prices", `[{"label":"","amount":150}]`) }, "Bad Request: Can't parse LabeledPrice: LabeledPrice label must be non-empty"},
		{"a price with no amount", func(f url.Values) { f.Set("prices", `[{"label":"x"}]`) }, `Bad Request: Can't parse LabeledPrice: Can't find field "amount"`},
		{"an amount that is not a number", func(f url.Values) { f.Set("prices", `[{"label":"x","amount":"lots"}]`) }, `Bad Request: Can't parse LabeledPrice: Field "amount" must be a valid Number`},
		{"no price in the list", func(f url.Values) { f.Set("prices", `[]`) }, "Bad Request: There must be at least one price part"},
		{"an amount past the largest", func(f url.Values) { f.Set("prices", `[{"label":"x","amount":1000000000000}]`) }, "Bad Request: Too big amount of the currency specified"},
		{"a total of zero", func(f url.Values) { f.Set("prices", `[{"label":"x","amount":0}]`) }, "Bad Request: Total price must be positive"},
		{"a discount past the price", func(f url.Values) {
			f.Set("currency", "USD")
			f.Set("prices", `[{"label":"Fee","amount":100},{"label":"Discount","amount":-150}]`)
		}, "Bad Request: Total price must be positive"},
		{"Stars with two prices", func(f url.Values) {
			f.Set("prices", `[{"label":"a","amount":1},{"label":"b","amount":2}]`)
		}, "Bad Request: prices must contain exactly one item"},
		{"a keyboard that does not lead with Pay", func(f url.Values) {
			f.Set("reply_markup", `{"inline_keyboard":[[{"text":"Cancel","callback_data":"cancel"}]]}`)
		}, "Bad Request: BUTTON_TYPE_INVALID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := starsInvoiceForm()
			c.mutate(form)
			status, body := s.call("sendInvoice", form)
			if status != http.StatusBadRequest || body["description"] != c.want {
				t.Fatalf("%s: %d %v, want %q", c.name, status, body, c.want)
			}
			if len(s.fake.Chat(4242).Messages) != 0 {
				t.Fatal("a refused invoice must not reach the chat")
			}
		})
	}
}

// A price line may be negative, a discount, as long as the total is not; an
// amount may come as a string of digits, as the Bot API server reads it.
func TestSendInvoice_TakesADiscountLine(t *testing.T) {
	s := newStand(t, Options{})
	form := starsInvoiceForm()
	form.Set("currency", "USD")
	form.Set("prices", `[{"label":"Fee","amount":"200"},{"label":"Discount","amount":-50}]`)
	status, body := s.call("sendInvoice", form)
	if status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	if invoice := result(t, body)["invoice"].(map[string]any); invoice["total_amount"] != float64(150) {
		t.Fatalf("invoice: %v", invoice)
	}
}

func TestSendInvoice_PayButtonIsOnlyLegalOnAnInvoice(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.call("sendMessage", url.Values{
		"chat_id": {"4242"}, "text": {"pay"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"Pay","pay":true}]]}`},
	})
	if status != http.StatusBadRequest || body["description"] != "Bad Request: BUTTON_TYPE_INVALID" {
		t.Fatalf("pay button outside an invoice: %d %v", status, body)
	}
}

func TestSendInvoice_RecordsWhatTheBotGaveIt(t *testing.T) {
	s := newStand(t, Options{})
	form := starsInvoiceForm()
	form.Set("currency", "USD")
	form.Set("prices", `[{"label":"Fee","amount":100},{"label":"Tax","amount":50}]`)
	status, body := s.call("sendInvoice", form)
	if status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	invoice := result(t, body)["invoice"].(map[string]any)
	if invoice["currency"] != "USD" || invoice["total_amount"] != float64(150) || invoice["title"] != "Premium" {
		t.Fatalf("invoice: %v", invoice)
	}
	if invoice["start_parameter"] != nil {
		t.Fatalf("start_parameter: %v", invoice)
	}
}

func TestAnswerPreCheckoutQuery_Refusals(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.call("answerPreCheckoutQuery", url.Values{"ok": {"true"}})
	if status != http.StatusBadRequest ||
		body["description"] != "Bad Request: query is too old and response timeout expired or query ID is invalid" {
		t.Fatalf("no id: %d %v", status, body)
	}
	status, body = s.call("answerPreCheckoutQuery", url.Values{
		"pre_checkout_query_id": {"pcq-1"}, "ok": {"true"},
	})
	if status != http.StatusBadRequest ||
		body["description"] != "Bad Request: query is too old and response timeout expired or query ID is invalid" {
		t.Fatalf("unknown query: %d %v", status, body)
	}
	status, body = s.call("answerPreCheckoutQuery", url.Values{
		"pre_checkout_query_id": {"pcq-1"}, "ok": {"false"},
	})
	if status != http.StatusBadRequest || body["description"] != `Bad Request: Parameter "error_message" is required` {
		t.Fatalf("a refusal with no message: %d %v", status, body)
	}
}

// ok is read the way the Bot API server reads a Boolean: a library that
// spells it "True" confirms the pre-checkout, it does not refuse it.
func TestAnswerPreCheckoutQuery_ReadsOkAsTelegramDoes(t *testing.T) {
	s := newStand(t, Options{})
	if status, body := s.call("sendInvoice", starsInvoiceForm()); status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	if status, body := s.sim("POST", "/sim/invoice/pay", map[string]any{"chat_id": 4242}); status != http.StatusOK {
		t.Fatalf("pay: %d %v", status, body)
	}
	status, body := s.call("answerPreCheckoutQuery", url.Values{"pre_checkout_query_id": {"pcq-1"}, "ok": {"True"}})
	if status != http.StatusOK {
		t.Fatalf("answerPreCheckoutQuery ok=True: %d %v", status, body)
	}
	if paid := successfulPayment(s); paid == nil || paid.TotalAmount != 150 {
		t.Fatalf("a confirmed pre-checkout takes the money: %v", paid)
	}
}

func TestPreCheckoutQuery_TakesOneAnswer(t *testing.T) {
	s := newStand(t, Options{})
	form := starsInvoiceForm()
	if status, body := s.call("sendInvoice", form); status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	if status, body := s.sim("POST", "/sim/invoice/pay", map[string]any{"chat_id": 4242}); status != http.StatusOK {
		t.Fatalf("pay: %d %v", status, body)
	}
	status, body := s.call("answerPreCheckoutQuery", url.Values{
		"pre_checkout_query_id": {"pcq-1"}, "ok": {"false"}, "error_message": {"gone"},
	})
	if status != http.StatusOK {
		t.Fatalf("first answer: %d %v", status, body)
	}
	status, body = s.call("answerPreCheckoutQuery", url.Values{
		"pre_checkout_query_id": {"pcq-1"}, "ok": {"true"},
	})
	if status != http.StatusBadRequest ||
		body["description"] != "Bad Request: query is too old and response timeout expired or query ID is invalid" {
		t.Fatalf("second answer: %d %v", status, body)
	}
	if paid := successfulPayment(s); paid != nil {
		t.Fatalf("a refused pre-checkout must take no money: %v", paid)
	}
}

func TestPreCheckoutQuery_CarriesTheInvoicePriceAndPayload(t *testing.T) {
	s := newStand(t, Options{})
	form := starsInvoiceForm()
	form.Set("currency", "USD")
	form.Set("prices", `[{"label":"Fee","amount":100},{"label":"Tax","amount":50}]`)
	if status, body := s.call("sendInvoice", form); status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	if status, body := s.sim("POST", "/sim/invoice/pay", map[string]any{"chat_id": 4242}); status != http.StatusOK {
		t.Fatalf("pay: %d %v", status, body)
	}
	_, body := s.call("getUpdates", nil)
	batch := updates(t, body)
	query, ok := batch[len(batch)-1]["pre_checkout_query"].(map[string]any)
	if !ok {
		t.Fatalf("no pre_checkout_query: %v", batch)
	}
	if query["currency"] != "USD" || query["total_amount"] != float64(150) || query["invoice_payload"] != "order-7" {
		t.Fatalf("query: %v", query)
	}
}

// allowed_updates keeps a pre_checkout_query out of a poll that did not ask
// for one, as it does for every other kind of update.
func TestPreCheckoutQuery_SubscriptionFiltersIt(t *testing.T) {
	s := newStand(t, Options{})
	if status, body := s.call("sendInvoice", starsInvoiceForm()); status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	s.fake.SetAllowedUpdates([]string{"message"})
	if status, body := s.sim("POST", "/sim/invoice/pay", map[string]any{"chat_id": 4242}); status != http.StatusOK {
		t.Fatalf("pay: %d %v", status, body)
	}
	if s.fake.PendingUpdates() != 0 {
		t.Fatalf("pending: %d, want 0", s.fake.PendingUpdates())
	}
}

func TestInjectPayment_Refusals(t *testing.T) {
	s := newStand(t, Options{})
	// No chat yet: nothing to deliver the payment to.
	status, body := s.sim("POST", "/sim/payment", map[string]any{"chat_id": 4242})
	if status != http.StatusNotFound || body["error"] != "chat 4242 has no messages" {
		t.Fatalf("no chat: %d %v", status, body)
	}
	if status, body := s.call("sendInvoice", starsInvoiceForm()); status != http.StatusOK {
		t.Fatalf("sendInvoice: %d %v", status, body)
	}
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"a refund with no charge id", map[string]any{"chat_id": 4242, "kind": "refunded"},
			"telegram_payment_charge_id is required for a refund"},
		{"a kind the stand does not know", map[string]any{"chat_id": 4242, "kind": "pending"},
			"kind is successful or refunded"},
	}
	for _, c := range cases {
		status, body := s.sim("POST", "/sim/payment", c.body)
		if status != http.StatusBadRequest || body["error"] != c.want {
			t.Fatalf("%s: %d %v, want %q", c.name, status, body, c.want)
		}
	}
	// The Go API refuses the same payments.
	if _, _, err := s.fake.InjectPayment(IncomingPayment{Kind: "pending"}); err == nil || err.Error() != "kind is successful or refunded" {
		t.Fatalf("InjectPayment with an unknown kind: %v", err)
	}
	if paid := successfulPayment(s); paid != nil {
		t.Fatalf("a refused payment must not reach the chat: %v", paid)
	}
	for _, m := range s.fake.Chat(4242).Messages {
		if m.RefundedPayment != nil {
			t.Fatalf("a refused refund must not reach the chat: %v", m.RefundedPayment)
		}
	}
}

// sendInvoice reads the type of its chat under the lock: an invoice racing
// the person's first message in another chat, which adds that chat, must not
// read the chats map unguarded.
func TestSendInvoice_SafeWhileAChatIsAdded(t *testing.T) {
	fake := New(Options{})
	defer fake.Close()
	handler := fake.Handler()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := int64(0); i < 50; i++ {
			fake.InjectMessage(IncomingMessage{ChatID: 10000 + i, UserID: 10000 + i, Text: "hi"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			form := starsInvoiceForm()
			form.Set("chat_id", strconv.Itoa(20000+i))
			req := httptest.NewRequest(http.MethodPost, "/bot123456:TOKEN/sendInvoice", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}
	}()
	wg.Wait()
	if got := len(fake.Calls("sendInvoice")); got != 50 {
		t.Fatalf("sendInvoice calls: %d, want 50", got)
	}
}
