// Command echobot is the smallest bot worth testing against tgfake, written
// with the standard library alone so nothing hides what goes over the wire. It
// long-polls getUpdates, answers a message with its text, offers a keyboard on
// /start and answers a tap on it by editing that message.
//
//	tgfake &
//	TELEGRAM_API=http://127.0.0.1:18790 BOT_TOKEN=123:fake go run ./examples/echobot
//	open http://127.0.0.1:18790/
//
// main_test.go is the same bot under test with the stand in-process.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	api := os.Getenv("TELEGRAM_API")
	if api == "" {
		api = "https://api.telegram.org"
	}
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("echobot: BOT_TOKEN is not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bot := &Bot{API: api, Token: token, PollTimeout: 30 * time.Second}
	if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal("echobot: ", err)
	}
}

// Bot talks to the Bot API at API, https://api.telegram.org or a stand.
type Bot struct {
	API         string
	Token       string
	PollTimeout time.Duration
	Client      *http.Client
}

type update struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		MessageID int `json:"message_id"`
		Chat      struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
	CallbackQuery *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message *struct {
			MessageID int `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

// Run polls for updates and handles them one by one until ctx ends.
func (b *Bot) Run(ctx context.Context) error {
	offset := 0
	for {
		var updates []update
		err := b.call(ctx, "getUpdates", url.Values{
			"offset":          {strconv.Itoa(offset)},
			"timeout":         {strconv.Itoa(int(b.PollTimeout / time.Second))},
			"allowed_updates": {`["message","callback_query"]`},
		}, &updates)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("echobot: getUpdates: %v", err)
			time.Sleep(time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if err := b.handle(ctx, u); err != nil {
				log.Printf("echobot: update %d: %v", u.UpdateID, err)
			}
		}
	}
}

func (b *Bot) handle(ctx context.Context, u update) error {
	switch {
	case u.Message != nil && strings.HasPrefix(u.Message.Text, "/start"):
		return b.call(ctx, "sendMessage", url.Values{
			"chat_id":      {strconv.FormatInt(u.Message.Chat.ID, 10)},
			"text":         {"Hi! Send me anything, or tap a button."},
			"reply_markup": {`{"inline_keyboard":[[{"text":"Ping","callback_data":"ping"},{"text":"Docs","url":"https://core.telegram.org/bots/api"}]]}`},
		}, nil)
	case u.Message != nil:
		return b.call(ctx, "sendMessage", url.Values{
			"chat_id":             {strconv.FormatInt(u.Message.Chat.ID, 10)},
			"text":                {"You said: " + u.Message.Text},
			"reply_to_message_id": {strconv.Itoa(u.Message.MessageID)},
		}, nil)
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		// Answer the tap first: Telegram shows a spinner on the button until
		// the bot does, and takes one answer per query.
		if err := b.call(ctx, "answerCallbackQuery", url.Values{"callback_query_id": {q.ID}, "text": {"Pong!"}}, nil); err != nil {
			return err
		}
		if q.Data != "ping" || q.Message == nil {
			return nil
		}
		return b.call(ctx, "editMessageText", url.Values{
			"chat_id":    {strconv.FormatInt(q.Message.Chat.ID, 10)},
			"message_id": {strconv.Itoa(q.Message.MessageID)},
			"text":       {"Pong."},
		}, nil)
	}
	return nil
}

// call posts a Bot API method as a form and decodes its result into out.
func (b *Bot) call(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := strings.TrimRight(b.API, "/") + "/bot" + b.Token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := b.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if !body.OK {
		return fmt.Errorf("%s: %s", method, body.Description)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body.Result, out)
}
