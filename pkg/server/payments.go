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

// priceMax is the number of prices Telegram takes in one invoice, and
// xtrCurrency is the code of Telegram Stars.
const (
	priceMax    = 100
	xtrCurrency = "XTR"
)

// invoice field limits, as the Bot API states them for sendInvoice.
const (
	invoiceTitleMax       = 32
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
	if problem == "" && params.Get("currency") == xtrCurrency {
		problem = xtrNeedsOnePrice(prices)
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
		Caption:        "",
		ReplyToMessage: quoted,
		ReplyMarkup:    markup,
		Invoice: &botapi.Invoice{
			Title:          params.Get("title"),
			Description:    params.Get("description"),
			StartParameter: params.Get("start_parameter"),
			Currency:       params.Get("currency"),
			TotalAmount:    total,
		},
		HasProtectedContent: params.Get("protect_content") == "true",
	}
	stored := chat.appendLocked(msg, true, "", false)
	stored.invoicePayload = params.Get("payload")
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

// invoiceFieldProblem refuses an invoice Telegram would not send: a required
// field with nothing in it or past the length the Bot API states. It returns
// the description, or "" for an invoice the stand takes.
func invoiceFieldProblem(params url.Values) string {
	if utf8.RuneCountInString(params.Get("title")) == 0 {
		return "Bad Request: title is empty"
	}
	if utf8.RuneCountInString(params.Get("title")) > invoiceTitleMax {
		return "Bad Request: title is too long"
	}
	if utf8.RuneCountInString(params.Get("description")) == 0 {
		return "Bad Request: description is empty"
	}
	if utf8.RuneCountInString(params.Get("description")) > invoiceDescriptionMax {
		return "Bad Request: description is too long"
	}
	if params.Get("payload") == "" {
		return "Bad Request: payload is empty"
	}
	if len(params.Get("payload")) > invoicePayloadMax {
		return "Bad Request: payload is too long"
	}
	if params.Get("currency") == "" {
		return "Bad Request: currency is empty"
	}
	return ""
}

// parsePrices reads the prices of an invoice: a JSON array of LabeledPrice,
// not empty, no more than priceMax of them, none of a negative amount, and
// exactly one for Telegram Stars.
func parsePrices(raw string) ([]botapi.LabeledPrice, string) {
	if strings.TrimSpace(raw) == "" {
		return nil, "Bad Request: can't parse prices JSON object"
	}
	var prices []botapi.LabeledPrice
	if json.Unmarshal([]byte(raw), &prices) != nil {
		return nil, "Bad Request: can't parse prices JSON object"
	}
	if len(prices) == 0 {
		return nil, "Bad Request: prices are empty"
	}
	if len(prices) > priceMax {
		return nil, "Bad Request: too many prices specified"
	}
	for _, p := range prices {
		if p.Amount < 0 {
			return nil, "Bad Request: prices are invalid"
		}
	}
	return prices, ""
}

// invoiceKeyboardProblem refuses a keyboard Telegram would not put under an
// invoice: Telegram shows a Pay button of its own when there is none, and when
// there is one the first button has to be the Pay button.
func invoiceKeyboardProblem(markup *botapi.InlineKeyboardMarkup) string {
	if markup == nil || len(markup.InlineKeyboard) == 0 {
		return ""
	}
	first := markup.InlineKeyboard[0]
	if len(first) == 0 || !first[0].Pay {
		return "Bad Request: BUTTON_TYPE_INVALID"
	}
	return ""
}

// xtrNeedsOnePrice is the refusal of an invoice in Telegram Stars whose prices
// are not exactly one item, which is what the Bot API requires.
func xtrNeedsOnePrice(prices []botapi.LabeledPrice) string {
	if len(prices) != 1 {
		return "Bad Request: prices must contain exactly one item"
	}
	return ""
}

func (s *Server) answerPreCheckoutQuery(w http.ResponseWriter, method string, params url.Values) {
	id := params.Get("pre_checkout_query_id")
	if id == "" {
		s.writeError(w, method, params, 400, "Bad Request: pre_checkout_query_id is empty", 0)
		return
	}
	ok := params.Get("ok") == "true"
	if !ok && params.Get("error_message") == "" {
		s.writeError(w, method, params, 400, "Bad Request: error_message is empty", 0)
		return
	}
	s.mu.Lock()
	// The query names no chat; the tap that minted it does. A query the stand
	// never issued, one minted before a reset and one already answered are
	// refused the way Telegram refuses an answer to a query it does not know.
	pending := s.preCheckouts[id]
	if pending == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, 400,
			"Bad Request: query is too old and response timeout expired or query ID is invalid", 0)
		return
	}
	delete(s.preCheckouts, id)
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
