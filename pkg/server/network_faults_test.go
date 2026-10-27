package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAddressedFaultAndLostResponse(t *testing.T) {
	s := newStand(t, Options{})
	status, _ := s.sim("POST", "/sim/fault", map[string]any{"method": "sendMessage", "chat_id": 1, "code": 403})
	if status != 200 {
		t.Fatal(status)
	}
	status, _ = s.call("sendMessage", url.Values{"chat_id": {"2"}, "text": {"hello"}})
	if status != 200 {
		t.Fatalf("other chat blocked: %d", status)
	}
	status, _ = s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"hello"}})
	if status != 403 {
		t.Fatal(status)
	}
	s.sim("POST", "/sim/fault", map[string]any{"method": "sendMessage", "chat_id": 1, "disconnect": true, "after_apply": true, "times": 1})
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post(s.srv.URL+"/bot1/sendMessage", "application/x-www-form-urlencoded", strings.NewReader("chat_id=1&text=lost"))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected network failure")
	}
	if len(s.fake.Chat(1).Messages) != 1 {
		t.Fatal("lost response did not apply operation")
	}
	_, log := s.sim("GET", "/sim/outbox?method=sendMessage", nil)
	calls := log["calls"].([]any)
	last := calls[len(calls)-1].(map[string]any)
	if last["applied"] != true || last["response_sent"] != false || last["fault"] == nil {
		t.Fatal(last)
	}
	s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"lost"}})
	if len(s.fake.Chat(1).Messages) != 2 {
		t.Fatal("retry should produce a duplicate")
	}
}

func TestFaultBeforeApplyAndCancelledDelay(t *testing.T) {
	s := newStand(t, Options{})
	s.sim("POST", "/sim/fault", map[string]any{"method": "sendMessage", "disconnect": true, "times": 1})
	resp, err := http.Post(s.srv.URL+"/bot1/sendMessage", "application/x-www-form-urlencoded", strings.NewReader("chat_id=1&text=lost"))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected disconnect")
	}
	if len(s.fake.Chat(1).Messages) != 0 {
		t.Fatal("pre-apply fault changed chat")
	}
	s.sim("POST", "/sim/fault", map[string]any{"method": "sendMessage", "chat_id": 1, "delay_ms": 1000, "times": 1})
	client := &http.Client{Timeout: 30 * time.Millisecond}
	resp, err = client.Post(s.srv.URL+"/bot1/sendMessage", "application/x-www-form-urlencoded", strings.NewReader("chat_id=1&text=slow"))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected timeout")
	}
	status, _ := s.call("sendMessage", url.Values{"chat_id": {"2"}, "text": {"fast"}})
	if status != 200 {
		t.Fatal(status)
	}
	if len(s.fake.Chat(1).Messages) != 0 {
		t.Fatal("cancelled delay applied operation")
	}
}

func TestFileDownloadFaultHasSeparateScope(t *testing.T) {
	s := newStand(t, Options{})
	// A document fixture uses the same authenticated download route as photos.
	s.fake.mu.Lock()
	s.fake.files["fixture"] = &storedFile{id: "fixture", kind: "document", data: []byte("file bytes"), mimeType: "text/plain"}
	s.fake.mu.Unlock()
	s.sim("POST", "/sim/fault", map[string]any{"target": "file", "disconnect": true, "times": 1, "path": "document/fixture"})
	// Bot catch-all faults do not consume or affect the download rule.
	s.sim("POST", "/sim/fault", map[string]any{"method": "*", "code": 403})
	address := s.srv.URL + "/file/bot1/document/fixture"
	downloadClient := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := downloadClient.Get(address)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected download disconnect")
	}
	resp, err = downloadClient.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if string(raw) != "file bytes" {
		t.Fatal(resp.StatusCode, string(raw))
	}
}

func TestCloseCancelsFaultWait(t *testing.T) {
	s := New(Options{})
	defer s.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// ErrAbortHandler is the transport abort signal, normally caught by net/http.
		defer func() {
			if value := recover(); value != nil && value != http.ErrAbortHandler {
				panic(value)
			}
		}()
		_, _ = s.beginFault(httptest.NewRecorder(), httptest.NewRequest("POST", "/bot1/sendMessage", nil), "sendMessage", nil, &Fault{DelayMS: 60000})
	}()
	s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel delay")
	}
	if calls := s.Calls(""); len(calls) != 1 || calls[0].Applied || calls[0].ResponseSent {
		t.Fatal(calls)
	}
}

func TestCancelledFileDelayHasNoResponse(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.SetFault(Fault{Target: "file", DelayMS: 60000, Times: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer func() {
		if value := recover(); value != http.ErrAbortHandler {
			t.Errorf("expected transport abort, got %v", value)
		}
	}()
	params := url.Values{"path": {"photo/fixture"}}
	_, _ = s.fake.beginFault(httptest.NewRecorder(), httptest.NewRequest("GET", "/file/bot1/photo/fixture", nil).WithContext(ctx), "file", params, s.fake.takeFileFault(params))
}

// A webhook reply runs its method outside net/http, where a lost response has
// no connection to abort: the fault is filed and the stand keeps running.
func TestDisconnectFaultOnAWebhookReply(t *testing.T) {
	for _, fault := range []map[string]any{
		{"method": "sendMessage", "disconnect": true, "times": 1},
		{"method": "sendMessage", "disconnect": true, "after_apply": true, "times": 1},
	} {
		s := newStand(t, Options{})
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			_, _ = io.Copy(io.Discard, req.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"method":"sendMessage","chat_id":4242,"text":"webhook reply"}`))
		}))
		s.call("setWebhook", url.Values{"url": {receiver.URL}})
		s.sim("POST", "/sim/fault", fault)
		s.fake.InjectMessage(IncomingMessage{Text: "hello"})
		if !s.fake.WaitCall("sendMessage", 1, time.Second) {
			t.Fatalf("%v: the webhook reply was not filed", fault)
		}
		call := s.fake.Calls("sendMessage")[0]
		if call.Fault == nil || call.ResponseSent || call.Applied != (fault["after_apply"] == true) {
			t.Fatalf("%v: %+v", fault, call)
		}
		receiver.Close()
	}
}

// A message that carries a reply keyboard, its removal or ForceReply cannot be
// edited: TDLib's can_edit_message refuses any reply_markup but an inline one.
func TestReplyControlMessagesCannotBeEdited(t *testing.T) {
	s := newStand(t, Options{})
	for i, markup := range []string{`{"keyboard":[["A"]]}`, `{"remove_keyboard":true}`, `{"force_reply":true}`} {
		s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}})
		id := strconv.Itoa(i + 1)
		for method, form := range map[string]url.Values{
			"editMessageText":        {"chat_id": {"4242"}, "message_id": {id}, "text": {"changed"}},
			"editMessageReplyMarkup": {"chat_id": {"4242"}, "message_id": {id}, "reply_markup": {`{"inline_keyboard":[[{"text":"Go","callback_data":"go"}]]}`}},
		} {
			if status, body := s.call(method, form); status != 400 || body["description"] != "Bad Request: message can't be edited" {
				t.Errorf("%s of %s: %d %v", method, markup, status, body)
			}
		}
	}
}

// A fault that could never fire is refused instead of turning into another.
func TestFaultThatCannotFireIsRefused(t *testing.T) {
	s := newStand(t, Options{})
	for _, f := range []map[string]any{
		{"target": "files", "disconnect": true},
		{"target": "file", "chat_id": 1, "disconnect": true},
		{"method": "sendMessage", "path": "document/file-1", "code": 500},
	} {
		if status, body := s.sim("POST", "/sim/fault", f); status != 400 || body["error"] == nil {
			t.Errorf("%v: %d %v", f, status, body)
		}
	}
	if faults := s.fake.Faults(); len(faults) != 0 {
		t.Fatalf("refused faults were scheduled: %+v", faults)
	}
}
