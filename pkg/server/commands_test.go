package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

func TestSetMyCommands_Refusals(t *testing.T) {
	s := newStand(t, Options{})
	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"commands that are not a list", url.Values{"commands": {`nope`}},
			"Bad Request: can't parse commands JSON object"},
		{"no commands", url.Values{},
			"Bad Request: can't parse commands JSON object"},
		{"a scope that is not JSON", url.Values{"commands": {`[]`}, "scope": {`{type:chat}`}},
			"Bad Request: Can't parse BotCommandScope JSON object"},
		{"a scope that is not an object", url.Values{"commands": {`[]`}, "scope": {`["chat"]`}},
			"Bad Request: Can't parse BotCommandScope: BotCommandScope must be an Object"},
		{"a scope of no known type", url.Values{"commands": {`[]`}, "scope": {`{"type":"everyone"}`}},
			"Bad Request: Can't parse BotCommandScope: Unsupported type specified"},
		{"a scope with no type", url.Values{"commands": {`[]`}, "scope": {`{"chat_id":1}`}},
			`Bad Request: Can't parse BotCommandScope: Can't find field "type"`},
		{"a scope whose type is not a string", url.Values{"commands": {`[]`}, "scope": {`{"type":true}`}},
			`Bad Request: Can't parse BotCommandScope: Field "type" must be of type String`},
		{"a chat scope with no chat", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat"}`}},
			`Bad Request: Can't parse BotCommandScope: Can't find field "chat_id"`},
		{"a chat scope with an empty chat", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat","chat_id":""}`}},
			"Bad Request: Can't parse BotCommandScope: Empty chat_id specified"},
		{"a chat scope naming a username", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat","chat_id":"@alice"}`}},
			"Bad Request: chat not found"},
		{"a chat_member scope with no user", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat_member","chat_id":-100}`}},
			`Bad Request: Can't parse BotCommandScope: Can't find field "user_id"`},
		{"a chat_member scope with a user that is not a number", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat_member","chat_id":-100,"user_id":"alice"}`}},
			`Bad Request: Can't parse BotCommandScope: Field "user_id" must be a valid Number`},
		{"a chat_member scope with user 0", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat_member","chat_id":-100,"user_id":0}`}},
			"Bad Request: Can't parse BotCommandScope: Invalid user_id specified"},
		{"an administrators scope of a private chat", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat_administrators","chat_id":4242}`}},
			"Bad Request: Can't use specified scope in private chats"},
		{"a member scope of a private chat", url.Values{"commands": {`[]`}, "scope": {`{"type":"chat_member","chat_id":4242,"user_id":4242}`}},
			"Bad Request: Can't use specified scope in private chats"},
		{"a language that is not two letters", url.Values{"commands": {`[]`}, "language_code": {"eng"}},
			"Bad Request: Invalid language code specified"},
		{"a language in capitals", url.Values{"commands": {`[]`}, "language_code": {"EN"}},
			"Bad Request: Invalid language code specified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body := s.call("setMyCommands", c.form)
			if status != http.StatusBadRequest || body["description"] != c.want {
				t.Fatalf("%s: %d %v, want %q", c.name, status, body, c.want)
			}
			if len(s.fake.Commands()) != 0 {
				t.Fatal("a refused registration must not be kept")
			}
		})
	}
}

func TestGetMyCommands_RefusesTheSameScopes(t *testing.T) {
	s := newStand(t, Options{})
	for _, method := range []string{"getMyCommands", "deleteMyCommands"} {
		status, body := s.call(method, url.Values{"scope": {`{"type":"chat"}`}})
		if status != http.StatusBadRequest || body["description"] != `Bad Request: Can't parse BotCommandScope: Can't find field "chat_id"` {
			t.Fatalf("%s with no chat: %d %v", method, status, body)
		}
		status, body = s.call(method, url.Values{"language_code": {"english"}})
		if status != http.StatusBadRequest || body["description"] != "Bad Request: Invalid language code specified" {
			t.Fatalf("%s with a bad language: %d %v", method, status, body)
		}
	}
}

// A scope reads the way the Bot API server reads it: chat_id as a number, a
// string of digits or the @username of a group, user_id as a number or a
// string of digits.
func TestSetMyCommands_ReadsIdsAsNumbersOrStrings(t *testing.T) {
	s := newStand(t, Options{})
	if status, body := s.sim("POST", "/sim/chat/configure", map[string]any{
		"chat": map[string]any{"id": -100, "type": "supergroup", "title": "Group", "username": "team"},
	}); status != http.StatusOK {
		t.Fatalf("configure: %d %v", status, body)
	}
	if status, body := s.call("setMyCommands", url.Values{
		"commands": {`[{"command":"kick","description":"Kick"}]`},
		"scope":    {`{"type":"chat_member","chat_id":"-100","user_id":"4243"}`},
	}); status != http.StatusOK {
		t.Fatalf("setMyCommands: %d %v", status, body)
	}
	_, body := s.call("getMyCommands", url.Values{"scope": {`{"type":"chat_member","chat_id":-100,"user_id":4243}`}})
	if list, _ := body["result"].([]any); len(list) != 1 {
		t.Fatalf("getMyCommands: %v", body)
	}
	if status, body := s.call("setMyCommands", url.Values{
		"commands": {`[{"command":"rules","description":"Rules"}]`},
		"scope":    {`{"type":"chat","chat_id":"@team"}`},
	}); status != http.StatusOK {
		t.Fatalf("setMyCommands by username: %d %v", status, body)
	}
	if got := s.fake.CommandsFor(botapi.BotCommandScope{Type: "chat", ChatID: -100}, ""); len(got) != 1 || got[0].Command != "rules" {
		t.Fatalf("the group's list by its id: %v", got)
	}
}

// The chain the Bot API documents is narrower scope before wider, and the
// language's own list before the one registered with no language. A list for
// another language answers nothing and leaves the wider scope to answer.
func TestCommandsFor_WalksTheDocumentedChain(t *testing.T) {
	s := newStand(t, Options{})
	register := func(scope, language, name string) {
		t.Helper()
		form := url.Values{"commands": {`[{"command":"` + name + `","description":"` + name + `"}]`}}
		if scope != "" {
			form.Set("scope", scope)
		}
		if language != "" {
			form.Set("language_code", language)
		}
		if status, body := s.call("setMyCommands", form); status != http.StatusOK {
			t.Fatalf("setMyCommands %s: %d %v", name, status, body)
		}
	}
	shown := func(scope, language string) string {
		t.Helper()
		form := url.Values{}
		if scope != "" {
			form.Set("scope", scope)
		}
		if language != "" {
			form.Set("language_code", language)
		}
		_, body := s.call("getMyCommands", form)
		list, _ := body["result"].([]any)
		if len(list) == 0 {
			return ""
		}
		first, _ := list[0].(map[string]any)
		command, _ := first["command"].(string)
		return command
	}

	// Nothing is registered: the chain runs out and answers an empty list.
	if got := shown("", ""); got != "" {
		t.Fatalf("no commands: %q", got)
	}

	register("", "", "default")
	register(`{"type":"all_private_chats"}`, "", "private")
	register(`{"type":"chat","chat_id":4242}`, "", "chat")
	register(`{"type":"chat","chat_id":4242}`, "en", "chat-en")

	// A private chat chain: chat+language, chat, all_private_chats, default.
	cases := []struct{ scope, language, want string }{
		{`{"type":"chat","chat_id":4242}`, "en", "chat-en"},
		{`{"type":"chat","chat_id":4242}`, "ru", "chat"},
		{`{"type":"chat","chat_id":4243}`, "en", "private"},
		{`{"type":"all_private_chats"}`, "en", "private"},
		{`{"type":"default"}`, "en", "default"},
		{``, "en", "default"},
	}
	for _, c := range cases {
		if got := shown(c.scope, c.language); got != c.want {
			t.Fatalf("chain %s %q: %q, want %q", c.scope, c.language, got, c.want)
		}
	}

	// A list for another language answers nothing and leaves a wider scope
	// to answer, rather than blocking it.
	register(`{"type":"chat","chat_id":5252}`, "en", "other-en")
	if got := shown(`{"type":"chat","chat_id":5252}`, "ru"); got != "private" {
		t.Fatalf("a language with no list: %q, want the wider scope's", got)
	}

	// A group has no all_private_chats in its chain.
	register(`{"type":"all_group_chats"}`, "", "group")
	register(`{"type":"chat","chat_id":-100}`, "", "group-chat")
	if got := shown(`{"type":"chat","chat_id":-100}`, "en"); got != "group-chat" {
		t.Fatalf("group chat: %q", got)
	}
	if got := shown(`{"type":"chat","chat_id":-200}`, "en"); got != "group" {
		t.Fatalf("another group: %q", got)
	}
	if got := shown(`{"type":"chat","chat_id":-100}`, ""); got != "group-chat" {
		t.Fatalf("group chat with no language: %q", got)
	}
}

// The administrator scopes sit in a group's chain and nowhere else, and a
// plain member never reaches them.
func TestCommandsFor_AdministratorScopesServeAdministrators(t *testing.T) {
	s := newStand(t, Options{})
	if status, body := s.sim("POST", "/sim/chat/configure", map[string]any{
		"chat":    map[string]any{"id": -100, "type": "supergroup", "title": "Group"},
		"members": []map[string]any{{"status": "creator", "user": map[string]any{"id": 4242, "first_name": "Alice"}}},
	}); status != http.StatusOK {
		t.Fatalf("configure: %d %v", status, body)
	}
	register := func(scope, name string) {
		t.Helper()
		form := url.Values{"commands": {`[{"command":"` + name + `","description":"` + name + `"}]`}}
		if scope != "" {
			form.Set("scope", scope)
		}
		if status, body := s.call("setMyCommands", form); status != http.StatusOK {
			t.Fatalf("setMyCommands %s: %d %v", name, status, body)
		}
	}
	shown := func(scope string) string {
		t.Helper()
		_, body := s.call("getMyCommands", url.Values{"scope": {scope}})
		list, _ := body["result"].([]any)
		if len(list) == 0 {
			return ""
		}
		first, _ := list[0].(map[string]any)
		command, _ := first["command"].(string)
		return command
	}

	register("", "default")
	register(`{"type":"chat_administrators","chat_id":-100}`, "admins")
	register(`{"type":"all_chat_administrators"}`, "all-admins")

	// The owner of the group reaches the chat's administrators.
	if got := shown(`{"type":"chat_member","chat_id":-100,"user_id":4242}`); got != "admins" {
		t.Fatalf("administrator: %q", got)
	}
	// A plain member skips the administrator scopes entirely.
	if got := shown(`{"type":"chat_member","chat_id":-100,"user_id":4243}`); got != "default" {
		t.Fatalf("member: %q", got)
	}
	// The scope that names administrators resolves for one.
	if got := shown(`{"type":"chat_administrators","chat_id":-100}`); got != "admins" {
		t.Fatalf("administrators scope: %q", got)
	}
	// The widest administrator scope is next in the chain once the chat's own
	// one is gone - for a chat_member query and for a chat_administrators one.
	if status, body := s.call("deleteMyCommands", url.Values{"scope": {`{"type":"chat_administrators","chat_id":-100}`}}); status != http.StatusOK {
		t.Fatalf("deleteMyCommands: %d %v", status, body)
	}
	if got := shown(`{"type":"chat_member","chat_id":-100,"user_id":4242}`); got != "all-admins" {
		t.Fatalf("after deleting the chat's list: %q", got)
	}
	if got := shown(`{"type":"chat_administrators","chat_id":-100}`); got != "all-admins" {
		t.Fatalf("administrators scope after deleting the chat's list: %q", got)
	}
	// A private chat has no administrator scopes in its chain at all.
	if got := s.fake.CommandsFor(botapi.BotCommandScope{Type: "chat", ChatID: 4242}, ""); len(got) != 1 || got[0].Command != "default" {
		t.Fatalf("private chat: %v", got)
	}
}

// Commands() is the default scope with no language: what a bot with no scoped
// lists shows everyone, and what a caller of the Go API has always read back.
func TestCommands_IsTheDefaultScope(t *testing.T) {
	s := newStand(t, Options{})
	if status, body := s.call("setMyCommands", url.Values{
		"commands": {`[{"command":"start","description":"Go"}]`},
		"scope":    {`{"type":"chat","chat_id":4242}`},
	}); status != http.StatusOK {
		t.Fatalf("setMyCommands: %d %v", status, body)
	}
	if len(s.fake.Commands()) != 0 {
		t.Fatalf("Commands() = %v, want the empty default list", s.fake.Commands())
	}
	if status, body := s.call("setMyCommands", url.Values{
		"commands": {`[{"command":"help","description":"Help"}]`},
	}); status != http.StatusOK {
		t.Fatalf("setMyCommands default: %d %v", status, body)
	}
	list := s.fake.Commands()
	if len(list) != 1 || list[0].Command != "help" {
		t.Fatalf("Commands() = %v, want the default list", list)
	}
	if lists := s.fake.CommandLists(); len(lists) != 2 {
		t.Fatalf("CommandLists() = %v, want both registrations", lists)
	}
}
