package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRichDraftLifetime(t *testing.T) {
	s := newStand(t, Options{})
	start := time.Now()
	var elapsed atomic.Int64
	s.fake.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	send := func(id, text string) {
		t.Helper()
		status, body := s.call("sendRichMessageDraft", url.Values{
			"chat_id": {"4242"}, "draft_id": {id}, "rich_message": {`{"markdown":"` + text + `"}`},
		})
		if status != http.StatusOK || body["ok"] != true {
			t.Fatalf("draft rejected: %d %v", status, body)
		}
	}

	send("1", "first")
	elapsed.Store(int64(20 * time.Second))
	send("1", "revised")
	send("2", "second")
	s.call("sendRichMessage", url.Values{"chat_id": {"4242"}, "rich_message": {`{"markdown":"final"}`}})
	elapsed.Store(int64(50*time.Second - time.Nanosecond))
	view := s.fake.Chat(4242)
	if len(view.Drafts) != 2 || view.Drafts[0].Revisions != 2 || view.Drafts[0].Markdown != "revised" {
		t.Fatalf("revision did not renew the lifetime: %+v", view.Drafts)
	}
	elapsed.Add(1)
	view = s.fake.Chat(4242)
	if len(view.Drafts) != 0 || len(view.Messages) != 1 || view.Messages[0].Text != "final" {
		t.Fatalf("expiry must remove only drafts: %+v", view)
	}
	s.fake.mu.Lock()
	remaining := len(s.fake.chats[4242].drafts)
	s.fake.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("read retained %d expired drafts in storage", remaining)
	}
	if len(s.fake.Calls("sendRichMessageDraft")) != 3 {
		t.Fatal("expiry removed the debugging history")
	}
}

func TestRichDraftWritePrunesWithoutChatReads(t *testing.T) {
	s := newStand(t, Options{})
	start := time.Now()
	var elapsed atomic.Int64
	s.fake.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	for _, id := range []string{"1", "2"} {
		s.call("sendRichMessageDraft", url.Values{
			"chat_id": {"4242"}, "draft_id": {id}, "rich_message": {`{"markdown":"partial"}`},
		})
	}
	elapsed.Store(int64(30 * time.Second))
	s.call("sendRichMessageDraft", url.Values{
		"chat_id": {"4242"}, "draft_id": {"1"}, "rich_message": {`{"markdown":"new preview"}`},
	})
	s.fake.mu.Lock()
	defer s.fake.mu.Unlock()
	drafts := s.fake.chats[4242].drafts
	if len(drafts) != 1 || drafts[1] == nil || drafts[1].revisions != 1 {
		t.Fatalf("write must prune expired previews before reusing an id: %+v", drafts)
	}
}

func streamStoppableDraft(t *testing.T, s *stand, draftID string, keepOnStop bool) {
	t.Helper()
	status, body := s.call("sendRichMessageDraft", url.Values{
		"chat_id":      {"4242"},
		"draft_id":     {draftID},
		"can_stop":     {"true"},
		"keep_on_stop": {strconv.FormatBool(keepOnStop)},
		"rich_message": {`{"markdown":"partial"}`},
	})
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("draft %s rejected: %d %v", draftID, status, body)
	}
}

func TestDraftStop_KeepOnStopKeepsThePreview(t *testing.T) {
	s := newStand(t, Options{})
	streamStoppableDraft(t, s, "3", true)

	text := s.fake.Chat(4242).Text()
	if !strings.Contains(text, "draft 3 (rev 1): partial\n    [Stop]\n") {
		t.Fatalf("the transcript shows no Stop button:\n%s", text)
	}

	if _, err := s.fake.StopDraft(DraftStop{DraftID: 3}); err != nil {
		t.Fatal(err)
	}
	drafts := s.fake.Chat(4242).Drafts
	if len(drafts) != 1 || drafts[0].DraftID != 3 {
		t.Fatalf("keep_on_stop must leave the preview in the chat: %+v", drafts)
	}

	if _, err := s.fake.StopDraft(DraftStop{DraftID: 3}); err == nil {
		t.Fatal("the kept preview still has a Stop button")
	}
	if n := s.fake.PendingUpdates(); n != 1 {
		t.Fatalf("a kept preview delivered %d stops, want 1", n)
	}
}

func TestDraftStop_Refusals(t *testing.T) {
	s := newStand(t, Options{})
	start := time.Now()
	var elapsed atomic.Int64
	s.fake.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	refused := func(stop map[string]any, want string) {
		t.Helper()
		status, body := s.sim("POST", "/sim/draft/stop", stop)
		if status != http.StatusNotFound || body["error"] != want {
			t.Fatalf("stop %v: %d %v, want %q", stop, status, body, want)
		}
	}

	refused(map[string]any{}, "chat 4242 has no draft with a Stop button")

	s.call("sendRichMessageDraft", url.Values{
		"chat_id":      {"4242"},
		"draft_id":     {"1"},
		"rich_message": {`{"markdown":"no button"}`},
	})
	refused(map[string]any{}, "chat 4242 has no draft with a Stop button")
	refused(map[string]any{"draft_id": 1}, "draft 1 in chat 4242 shows no Stop button")
	refused(map[string]any{"draft_id": 2}, "draft 2 not found in chat 4242")

	streamStoppableDraft(t, s, "2", false)
	elapsed.Store(int64(draftLifetime))
	refused(map[string]any{"draft_id": 2}, "draft 2 not found in chat 4242")

	if n := s.fake.PendingUpdates(); n != 0 {
		t.Fatalf("a refused stop queued %d updates", n)
	}
}

func TestDraftStop_FollowsTheSubscription(t *testing.T) {
	s := newStand(t, Options{AllowedUpdates: []string{"message", "callback_query"}})
	streamStoppableDraft(t, s, "4", false)
	if _, err := s.fake.StopDraft(DraftStop{}); err != nil {
		t.Fatal(err)
	}
	if n := s.fake.PendingUpdates(); n != 0 {
		t.Fatalf("an unsubscribed stop was queued (%d)", n)
	}

	s.fake.SetAllowedUpdates([]string{"message", "stopped_message_generation"})
	streamStoppableDraft(t, s, "5", false)
	if _, err := s.fake.StopDraft(DraftStop{}); err != nil {
		t.Fatal(err)
	}
	_, body := s.call("getUpdates", nil)
	batch := updates(t, body)
	if len(batch) != 1 || batch[0]["stopped_message_generation"] == nil {
		t.Fatalf("a subscribed stop was not delivered: %v", batch)
	}
}
