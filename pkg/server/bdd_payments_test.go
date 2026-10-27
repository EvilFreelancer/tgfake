package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
	"github.com/cucumber/godog"
)

func TestPaymentsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "Bot payments",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var stand *stand
			var queryID string
			sc.Given(`^an offline Telegram stand$`, func() {
				stand = newStand(t, Options{})
			})
			sc.When(`^the bot sends an invoice for 150 Stars with payload order-7$`, func() error {
				return starsInvoice(stand, url.Values{})
			})
			sc.When(`^the bot sends an invoice with a Pay button and a cancel button$`, func() error {
				return starsInvoice(stand, url.Values{
					"reply_markup": {`{"inline_keyboard":[[{"text":"Pay 150","pay":true},{"text":"Cancel","callback_data":"cancel"}]]}`},
				})
			})
			sc.Then(`^the chat shows an invoice for 150 Stars$`, func() error {
				invoice := lastInvoice(stand)
				if invoice == nil || invoice.Currency != "XTR" || invoice.TotalAmount != 150 {
					return fmt.Errorf("invoice: %v", invoice)
				}
				return nil
			})
			sc.Then(`^the invoice keeps both buttons$`, func() error {
				messages := stand.fake.Chat(4242).Messages
				if len(messages) != 1 {
					return fmt.Errorf("messages: %v", messages)
				}
				keyboard := messages[0].Keyboard
				if len(keyboard) != 1 || len(keyboard[0]) != 2 || !keyboard[0][0].Pay || keyboard[0][1].CallbackData != "cancel" {
					return fmt.Errorf("keyboard: %v", keyboard)
				}
				return nil
			})
			sc.When(`^the person pays it$`, func() error {
				status, body := stand.sim("POST", "/sim/invoice/pay", map[string]any{"chat_id": 4242})
				if status != 200 {
					return fmt.Errorf("pay: %d %v", status, body)
				}
				queryID, _ = body["pre_checkout_query_id"].(string)
				if queryID == "" {
					return fmt.Errorf("no pre_checkout_query_id: %v", body)
				}
				return nil
			})
			sc.Then(`^the pre-checkout query carries 150 Stars and payload order-7$`, func() error {
				_, body := stand.call("getUpdates", nil)
				batch := updates(stand.t, body)
				if len(batch) == 0 {
					return fmt.Errorf("no update delivered")
				}
				query, ok := batch[len(batch)-1]["pre_checkout_query"].(map[string]any)
				if !ok {
					return fmt.Errorf("no pre_checkout_query in %v", batch)
				}
				if query["id"] != queryID || query["currency"] != "XTR" ||
					query["total_amount"] != float64(150) || query["invoice_payload"] != "order-7" {
					return fmt.Errorf("query: %v", query)
				}
				return nil
			})
			sc.When(`^the bot confirms the pre-checkout$`, func() error {
				return answerCheckout(stand, queryID, "true", "")
			})
			sc.When(`^the bot refuses the pre-checkout$`, func() error {
				return answerCheckout(stand, queryID, "false", "gone")
			})
			sc.When(`^the charge is returned$`, func() error {
				status, body := stand.sim("POST", "/sim/payment", map[string]any{
					"chat_id": 4242, "kind": "refunded", "telegram_payment_charge_id": "charge-1",
				})
				if status != 200 {
					return fmt.Errorf("refund: %d %v", status, body)
				}
				return nil
			})
			sc.Then(`^the chat shows a successful payment of 150 Stars for order-7$`, func() error {
				paid := successfulPayment(stand)
				if paid == nil {
					return fmt.Errorf("no successful payment in %v", stand.fake.Chat(4242).Messages)
				}
				if paid.Currency != "XTR" || paid.TotalAmount != 150 || paid.InvoicePayload != "order-7" {
					return fmt.Errorf("payment: %v", paid)
				}
				if !strings.HasPrefix(paid.TelegramPaymentChargeID, "charge-") {
					return fmt.Errorf("charge id: %q", paid.TelegramPaymentChargeID)
				}
				return nil
			})
			sc.Then(`^the chat shows a refund of 150 Stars for order-7$`, func() error {
				for _, m := range stand.fake.Chat(4242).Messages {
					if r := m.RefundedPayment; r != nil {
						if r.Currency == "XTR" && r.TotalAmount == 150 && r.InvoicePayload == "order-7" {
							return nil
						}
						return fmt.Errorf("refund: %v", r)
					}
				}
				return fmt.Errorf("no refund in %v", stand.fake.Chat(4242).Messages)
			})
			sc.Then(`^the chat shows no payment$`, func() error {
				if paid := successfulPayment(stand); paid != nil {
					return fmt.Errorf("unexpected payment: %v", paid)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/payments.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("payments feature failed")
	}
}

// starsInvoice sends the invoice the payment scenarios pay: 150 Stars for the
// payload order-7, with extra parameters the caller adds.
func starsInvoice(stand *stand, extra url.Values) error {
	form := url.Values{
		"chat_id":     {"4242"},
		"title":       {"Premium"},
		"description": {"One month"},
		"payload":     {"order-7"},
		"currency":    {"XTR"},
		"prices":      {`[{"label":"Premium","amount":150}]`},
	}
	for key, values := range extra {
		form[key] = values
	}
	status, body := stand.call("sendInvoice", form)
	if status != 200 {
		return fmt.Errorf("sendInvoice: %d %v", status, body)
	}
	return nil
}

func answerCheckout(stand *stand, id, ok, message string) error {
	form := url.Values{"pre_checkout_query_id": {id}, "ok": {ok}}
	if message != "" {
		form.Set("error_message", message)
	}
	status, body := stand.call("answerPreCheckoutQuery", form)
	if status != 200 {
		return fmt.Errorf("answerPreCheckoutQuery: %d %v", status, body)
	}
	return nil
}

func lastInvoice(stand *stand) *botapi.Invoice {
	messages := stand.fake.Chat(4242).Messages
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Invoice != nil {
			return messages[i].Invoice
		}
	}
	return nil
}

func successfulPayment(stand *stand) *botapi.SuccessfulPayment {
	for _, m := range stand.fake.Chat(4242).Messages {
		if m.SuccessfulPayment != nil {
			return m.SuccessfulPayment
		}
	}
	return nil
}
