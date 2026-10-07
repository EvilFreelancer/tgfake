package main

// The bot of main.go under test, the stand in-process: the person's side is
// driven through the Go API of the stand (InjectMessage, InjectCallback), the
// bot runs its own polling loop against the stand's handler, and what reached
// the chat is read back as the person would see it.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/server"
)

func startBot(t *testing.T) *server.Server {
	t.Helper()
	fake := server.New(server.Options{MaxPollWait: 200 * time.Millisecond})
	srv := httptest.NewServer(fake.Handler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		bot := &Bot{API: srv.URL, Token: "123:TEST", PollTimeout: time.Second, Client: &http.Client{}}
		_ = bot.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		fake.Close() // release held polls before the listener closes
		srv.Close()
	})
	return fake
}

func TestEchoesAMessageAsAReply(t *testing.T) {
	fake := startBot(t)
	_, msgID := fake.InjectMessage(server.IncomingMessage{Text: "hello"})
	if !fake.WaitCall("sendMessage", 1, 5*time.Second) {
		t.Fatalf("the bot did not answer:\n%s", fake.Chat(4242).Text())
	}
	msgs := fake.Chat(4242).Messages
	last := msgs[len(msgs)-1]
	if last.From != "bot" || last.Text != "You said: hello" || last.ReplyToMessageID != msgID {
		t.Fatalf("the bot answered %+v", last)
	}
}

func TestATapOnPingEditsTheMessage(t *testing.T) {
	fake := startBot(t)
	fake.InjectMessage(server.IncomingMessage{Text: "/start"})
	if !fake.WaitCall("sendMessage", 1, 5*time.Second) {
		t.Fatal("no greeting")
	}
	if _, _, err := fake.InjectCallback(server.IncomingCallback{Label: "Ping"}); err != nil {
		t.Fatalf("tap: %v\n%s", err, fake.Chat(4242).Text())
	}
	if !fake.WaitCall("editMessageText", 1, 5*time.Second) {
		t.Fatalf("the tap was not handled:\n%s", fake.Chat(4242).Text())
	}
	chat := fake.Chat(4242)
	if got := chat.Text(); !strings.Contains(got, "Pong.") {
		t.Fatalf("chat:\n%s", got)
	}
	if len(chat.Callbacks) != 1 || chat.Callbacks[0].Text != "Pong!" {
		t.Fatalf("the tap was answered %+v", chat.Callbacks)
	}
}

// The stand refuses what Telegram refuses, so a bug a real chat would show
// shows here first: an answer to a callback query nobody sent fails.
func TestTheStandRefusesAnUnknownCallbackQuery(t *testing.T) {
	fake := server.New(server.Options{})
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(func() {
		fake.Close()
		srv.Close()
	})
	bot := &Bot{API: srv.URL, Token: "123:TEST"}
	err := bot.call(context.Background(), "answerCallbackQuery", url.Values{"callback_query_id": {"nope"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "query ID is invalid") {
		t.Fatalf("an answer to a query the stand never issued: %v", err)
	}
}
