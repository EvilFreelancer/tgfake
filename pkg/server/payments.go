package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// Payments are the invoice a bot sends and the charge Telegram reports back.
// The bot only ever sees three things: its own sendInvoice, the
// pre_checkout_query the person's tap on Pay produces, and the service message
// that carries the charge once it is taken or returned. The stand models those
// three and nothing of the provider: a card gateway, shipping and order info
// are outside what a bot can observe before the charge.
//
//	sendInvoice               a Message with an Invoice
//	answerPreCheckoutQuery    one answer per query, as for a callback
//	pre_checkout_query        an update of its own kind
//	successful_payment        a service message in the chat
//	refunded_payment          a service message in the chat

// xtrCurrency is the code of Telegram Stars, and amountMax the largest
// amount, in the smallest units of a currency, Telegram takes in a price.
const (
	xtrCurrency = "XTR"
	amountMax   = 999_999_999_999
)

// What Telegram keeps of an invoice: a title is cut to 64 characters and a
// description to 255, while a payload past 128 bytes is refused.
const (
	invoiceTitleMax       = 64
	invoiceDescriptionMax = 255
	invoicePayloadMax     = 128 // bytes
)

// preCheckout is one pre_checkout_query the stand issued and has not yet seen
// answered. A query takes exactly one answer, like a callback query: the
// stand forgets it once answered.
type preCheckout struct {
	chat  int64
	query botapi.PreCheckoutQuery
}

func (s *Server) sendInvoice(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, 400, "Bad Request: chat_id is empty", 0)
		return
	}
	problem := invoiceFieldProblem(params)
	prices := []botapi.LabeledPrice{}
	if problem == "" {
		prices, problem = parsePrices(params.Get("prices"))
	}
	if problem == "" {
		problem = invoiceLimitProblem(params, prices)
	}
	markup := (*botapi.InlineKeyboardMarkup)(nil)
	if problem == "" {
		markup, problem = parseKeyboard(params.Get("reply_markup"))
	}
	if problem == "" {
		s.mu.Lock()
		chatType := s.chatTypeLocked(chatID)
		s.mu.Unlock()
		problem = validateKeyboard(markup, chatType, true)
	}
	if problem == "" {
		problem = invoiceKeyboardProblem(markup)
	}
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	quoted, problem := replyTargetLocked(chat, params)
	if problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	var total int64
	for _, p := range prices {
		total += p.Amount
	}
	msg := &botapi.Message{
		From:           s.botUser(),
		Chat:           chat.wire(),
		Date:           s.now().Unix(),
		ReplyToMessage: quoted,
		ReplyMarkup:    markup,
		Invoice: &botapi.Invoice{
			Title:          cutRunes(params.Get("title"), invoiceTitleMax),
			Description:    cutRunes(params.Get("description"), invoiceDescriptionMax),
			StartParameter: params.Get("start_parameter"),
			Currency:       params.Get("currency"),
			TotalAmount:    total,
		},
		HasProtectedContent: truthy(params.Get("protect_content")),
	}
	stored := chat.appendLocked(msg, true, "", false)
	chat.applyReplyMarkupLocked(params.Get("reply_markup"), stored)
	stored.invoicePayload = params.Get("payload")
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

// invoiceRequired are the parameters of sendInvoice the Bot API server
// refuses to go without, in the order it reads them.
var invoiceRequired = []string{"title", "description", "payload", "currency", "prices"}

// invoiceFieldProblem refuses an invoice the Bot API server does not read: a
// required parameter with nothing in it, or a payload that is not UTF-8. It
// returns the description, or "" for an invoice the stand takes.
func invoiceFieldProblem(params url.Values) string {
	for _, name := range invoiceRequired {
		if params.Get(name) == "" {
			return `Bad Request: parameter "` + name + `" is required`
		}
		if name == "payload" && !utf8.ValidString(params.Get(name)) {
			return "Bad Request: the payload must be encoded in UTF-8"
		}
	}
	return ""
}

// invoiceLimitProblem refuses an invoice Telegram refuses once it has read
// it: a title of nothing but spaces, a payload past 128 bytes, a currency
// other than Telegram Stars with no payment provider, and an invoice in Stars
// whose prices are not exactly one item.
func invoiceLimitProblem(params url.Values, prices []botapi.LabeledPrice) string {
	switch {
	case strings.TrimSpace(params.Get("title")) == "":
		return "Bad Request: INVOICE_TITLE_INVALID"
	case len(params.Get("payload")) > invoicePayloadMax:
		return "Bad Request: INVOICE_PAYLOAD_INVALID"
	case params.Get("currency") != xtrCurrency && params.Get("provider_token") == "":
		return "Bad Request: PAYMENT_PROVIDER_INVALID"
	case params.Get("currency") == xtrCurrency && len(prices) != 1:
		return "Bad Request: STARS_INVOICE_INVALID"
	}
	return ""
}

// cutRunes keeps the first max characters of text, the way Telegram cuts an
// invoice's title and description instead of refusing them.
func cutRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// parsePrices reads the prices of an invoice the way the Bot API server and
// Telegram read them: a JSON array of LabeledPrice objects, each with a
// non-empty label and an integer amount (a number or a string of digits).
// A single amount may be negative, a discount, but the total has to be
// positive. It returns the prices and the description of the refusal, if any.
func parsePrices(raw string) ([]botapi.LabeledPrice, string) {
	if !json.Valid([]byte(raw)) {
		return nil, "Bad Request: can't parse prices JSON object"
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil, "Bad Request: expected an Array of LabeledPrice"
	}
	prices := make([]botapi.LabeledPrice, 0, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &fields) != nil || fields == nil {
			return nil, "Bad Request: can't parse LabeledPrice: LabeledPrice must be an Object"
		}
		label, problem := jsonStringField(fields, "label")
		if problem == "" && label == "" {
			problem = "LabeledPrice label must be non-empty"
		}
		if problem != "" {
			return nil, "Bad Request: can't parse LabeledPrice: " + problem
		}
		amount, problem := jsonIntegerField(fields, "amount")
		if problem != "" {
			return nil, "Bad Request: can't parse LabeledPrice: " + problem
		}
		prices = append(prices, botapi.LabeledPrice{Label: label, Amount: amount})
	}
	if len(prices) == 0 {
		return nil, "Bad Request: there must be at least one price part"
	}
	var total int64
	for _, p := range prices {
		if p.Amount < -amountMax || p.Amount > amountMax {
			return nil, "Bad Request: too big amount of the currency specified"
		}
		total += p.Amount
	}
	if total <= 0 {
		return nil, "Bad Request: total price must be positive"
	}
	if total > amountMax {
		return nil, "Bad Request: total price is too big"
	}
	return prices, ""
}

// invoiceKeyboardProblem refuses a keyboard Telegram would not put under an
// invoice: Telegram shows a Pay button of its own when there is no keyboard,
// and a keyboard has to lead with one - REPLY_MARKUP_BUY_EMPTY when it has
// none, BUTTON_POS_INVALID when it has one elsewhere.
func invoiceKeyboardProblem(markup *botapi.InlineKeyboardMarkup) string {
	if markup == nil || len(markup.InlineKeyboard) == 0 {
		return ""
	}
	if first := markup.InlineKeyboard[0]; len(first) > 0 && first[0].Pay {
		return ""
	}
	for _, row := range markup.InlineKeyboard {
		for _, b := range row {
			if b.Pay {
				return "Bad Request: BUTTON_POS_INVALID"
			}
		}
	}
	return "Bad Request: REPLY_MARKUP_BUY_EMPTY"
}

func (s *Server) answerPreCheckoutQuery(w http.ResponseWriter, method string, params url.Values) {
	ok := truthy(params.Get("ok"))
	if !ok && params.Get("error_message") == "" {
		s.writeError(w, method, params, 400, `Bad Request: parameter "error_message" is required`, 0)
		return
	}
	s.mu.Lock()
	// The query names no chat; the tap that minted it does. A query the stand
	// never issued, an empty id, one minted before a reset and one already
	// answered are refused the way Telegram refuses an answer to a query it
	// does not know.
	pending := s.preCheckouts[params.Get("pre_checkout_query_id")]
	if pending == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, 400,
			"Bad Request: query is too old and response timeout expired or query ID is invalid", 0)
		return
	}
	delete(s.preCheckouts, params.Get("pre_checkout_query_id"))
	if ok {
		s.chargeLocked(pending)
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// chargeLocked delivers the successful_payment service message Telegram sends
// once a pre-checkout is confirmed and the money is taken. Caller holds s.mu.
func (s *Server) chargeLocked(pending *preCheckout) {
	q := pending.query
	s.nextCharge++
	payment := &botapi.SuccessfulPayment{
		Currency:                q.Currency,
		TotalAmount:             q.TotalAmount,
		InvoicePayload:          q.InvoicePayload,
		TelegramPaymentChargeID: "charge-" + strconv.Itoa(s.nextCharge),
		ProviderPaymentChargeID: "provider-charge-" + strconv.Itoa(s.nextCharge),
	}
	s.appendPaymentLocked(pending.chat, &q.From, payment, nil)
}

// appendPaymentLocked stores a payment service message and queues it as the
// message update Telegram sends for it. Caller holds s.mu.
func (s *Server) appendPaymentLocked(chatID int64, from *botapi.User, payment *botapi.SuccessfulPayment, refund *botapi.RefundedPayment) (int, int) {
	chat := s.ensureChatLocked(chatID, "", "", nil)
	msg := &botapi.Message{
		From:              from,
		Chat:              chat.wire(),
		Date:              s.now().Unix(),
		SuccessfulPayment: payment,
		RefundedPayment:   refund,
	}
	stored := chat.appendLocked(msg, false, "", false)
	return s.pushUpdateLocked(botapi.Update{Message: stored.clone()}), stored.msg.MessageID
}

// InvoicePay is the person's tap on the Pay button of an invoice.
type InvoicePay struct {
	ChatID    int64  `json:"chat_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	// MessageID names the invoice to pay; the newest one carrying an invoice
	// is the one when it is left out.
	MessageID int `json:"message_id,omitempty"`
}

// PayInvoice mints the pre_checkout_query Telegram sends when the person
// starts paying. The query carries the currency, the total and the payload of
// the invoice itself, so a bot that re-checks the price against its own
// catalogue meets whatever the invoice said - including a price that has
// moved on since.
func (s *Server) PayInvoice(in InvoicePay) (updateID int, queryID string, err error) {
	if in.UserID == 0 {
		in.UserID = defaultUserID
	}
	if in.Username == "" && in.UserID == defaultUserID {
		in.Username = defaultUsername
	}
	if in.FirstName == "" {
		in.FirstName = defaultFirstName
	}
	if in.ChatID == 0 {
		in.ChatID = in.UserID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil {
		return 0, "", fmt.Errorf("chat %d has no messages", in.ChatID)
	}
	target, err := s.invoiceToPayLocked(chat, in.MessageID)
	if err != nil {
		return 0, "", err
	}
	s.nextPreCheckout++
	id := "pcq-" + strconv.Itoa(s.nextPreCheckout)
	from := botapi.User{ID: in.UserID, FirstName: in.FirstName, Username: in.Username}
	query := botapi.PreCheckoutQuery{
		ID:             id,
		From:           from,
		Currency:       target.msg.Invoice.Currency,
		TotalAmount:    target.msg.Invoice.TotalAmount,
		InvoicePayload: target.invoicePayload,
	}
	s.preCheckouts[id] = &preCheckout{chat: in.ChatID, query: query}
	return s.pushUpdateLocked(botapi.Update{PreCheckoutQuery: &query}), id, nil
}

// invoiceToPayLocked finds the invoice a Pay press is for: the one MessageID
// names, else the newest message still carrying an invoice.
func (s *Server) invoiceToPayLocked(chat *chatState, messageID int) (*storedMessage, error) {
	if messageID != 0 {
		target := chat.byID[messageID]
		if target == nil || target.deleted || target.msg.Invoice == nil {
			return nil, fmt.Errorf("message %d not found in chat %d", messageID, chat.id)
		}
		return target, nil
	}
	for i := len(chat.messages) - 1; i >= 0; i-- {
		m := chat.messages[i]
		if !m.deleted && m.msg.Invoice != nil {
			return m, nil
		}
	}
	return nil, fmt.Errorf("chat %d has no message with an invoice", chat.id)
}

// IncomingPayment is a payment service message Telegram delivers on its own:
// a charge taken outside a pre-checkout this stand watched, or a charge
// returned. A zero total and currency take what the newest invoice said.
type IncomingPayment struct {
	ChatID    int64  `json:"chat_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	// Kind is "successful" (the default) or "refunded".
	Kind                    string `json:"kind,omitempty"`
	Currency                string `json:"currency,omitempty"`
	TotalAmount             int64  `json:"total_amount,omitempty"`
	InvoicePayload          string `json:"invoice_payload,omitempty"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id,omitempty"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id,omitempty"`
}

// problem says why a payment cannot be delivered whatever the chat holds: a
// kind the stand does not know, or a refund with no charge to return. It
// returns "" for a payment the stand can deliver.
func (in IncomingPayment) problem() string {
	switch strings.ToLower(in.Kind) {
	case "", "successful":
		return ""
	case "refunded":
		if in.TelegramPaymentChargeID == "" {
			return "telegram_payment_charge_id is required for a refund"
		}
		return ""
	}
	return "kind is successful or refunded"
}

// InjectPayment delivers a payment service message and returns the update id
// and the message id.
func (s *Server) InjectPayment(in IncomingPayment) (updateID, messageID int, err error) {
	if problem := in.problem(); problem != "" {
		return 0, 0, errors.New(problem)
	}
	if in.UserID == 0 {
		in.UserID = defaultUserID
	}
	if in.Username == "" && in.UserID == defaultUserID {
		in.Username = defaultUsername
	}
	if in.FirstName == "" {
		in.FirstName = defaultFirstName
	}
	if in.ChatID == 0 {
		in.ChatID = in.UserID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.chats[in.ChatID]
	if chat == nil {
		return 0, 0, fmt.Errorf("chat %d has no messages", in.ChatID)
	}
	invoice := s.newestInvoiceLocked(chat)
	if in.Currency == "" {
		in.Currency = xtrCurrency
		if invoice != nil {
			in.Currency = invoice.msg.Invoice.Currency
		}
	}
	if in.TotalAmount == 0 && invoice != nil {
		in.TotalAmount = invoice.msg.Invoice.TotalAmount
	}
	if in.InvoicePayload == "" && invoice != nil {
		in.InvoicePayload = invoice.invoicePayload
	}
	if strings.EqualFold(in.Kind, "refunded") {
		refund := &botapi.RefundedPayment{
			Currency:                in.Currency,
			TotalAmount:             in.TotalAmount,
			InvoicePayload:          in.InvoicePayload,
			TelegramPaymentChargeID: in.TelegramPaymentChargeID,
			ProviderPaymentChargeID: in.ProviderPaymentChargeID,
		}
		upd, id := s.appendPaymentLocked(in.ChatID, paymentUser(in), nil, refund)
		return upd, id, nil
	}
	s.nextCharge++
	charge := in.TelegramPaymentChargeID
	if charge == "" {
		charge = "charge-" + strconv.Itoa(s.nextCharge)
	}
	provider := in.ProviderPaymentChargeID
	if provider == "" {
		provider = "provider-charge-" + strconv.Itoa(s.nextCharge)
	}
	payment := &botapi.SuccessfulPayment{
		Currency:                in.Currency,
		TotalAmount:             in.TotalAmount,
		InvoicePayload:          in.InvoicePayload,
		TelegramPaymentChargeID: charge,
		ProviderPaymentChargeID: provider,
	}
	upd, id := s.appendPaymentLocked(in.ChatID, paymentUser(in), payment, nil)
	return upd, id, nil
}

func paymentUser(in IncomingPayment) *botapi.User {
	return &botapi.User{ID: in.UserID, FirstName: in.FirstName, Username: in.Username}
}

func (s *Server) newestInvoiceLocked(chat *chatState) *storedMessage {
	for i := len(chat.messages) - 1; i >= 0; i-- {
		m := chat.messages[i]
		if !m.deleted && m.msg.Invoice != nil {
			return m
		}
	}
	return nil
}
