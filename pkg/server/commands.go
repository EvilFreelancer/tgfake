package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// A bot keeps one list of commands per scope and per user language, as
// setMyCommands registers them: the list of a narrow scope is what a user of
// that scope sees, and a list registered with no language_code answers every
// user "for whose language there are no dedicated commands". deleteMyCommands
// takes one list away and lets a wider one show through again.

// commandScope is a BotCommandScope normalised for storage and lookup.
type commandScope struct {
	kind   string
	chatID int64
	userID int64
}

// defaultCommandScope is the scope a call names when it passes none.
var defaultCommandScope = commandScope{kind: "default"}

// commandKey is one registered list: a scope and a user language. An empty
// language is the list Telegram hands every language without a dedicated one.
type commandKey struct {
	scope commandScope
	lang  string
}

// specificity is how narrow a scope is. A wider one never wins against a
// narrower one that fits the user; Telegram's own order, widest first:
// default, all_private_chats/all_group_chats, all_chat_administrators, chat,
// chat_administrators, chat_member.
func (c commandScope) specificity() int {
	switch c.kind {
	case "chat_member":
		return 6
	case "chat_administrators":
		return 5
	case "chat":
		return 4
	case "all_chat_administrators":
		return 3
	case "all_private_chats", "all_group_chats":
		return 2
	default:
		return 1
	}
}

// isGroupChat reports whether a chat is a group, supergroup or channel: the
// chats Telegram numbers negatively.
func isGroupChat(typ string) bool { return typ != "private" }

// covers reports whether a user the query scope names would also read what is
// registered for c: the same audience, or a wider one. chatType is the type of
// the chat the query names, and admin says the query is about an
// administrator - the two things Telegram's chain of command lists turns on.
//
// The chain the Bot API documents ("Determining list of commands") is exactly
// this relation ranked by specificity, with the administrator scopes left out
// for anyone who is not one:
//
//	chatMember, chatAdministrators (admins), chat,
//	allChatAdministrators (admins), allGroupChats, default
//
// in a group, and chat, allPrivateChats, default in a private chat, each tried
// with the user's language before without one.
func (c commandScope) covers(q commandScope, chatType string, admin bool) bool {
	switch c.kind {
	case "default":
		return true
	case "chat_member":
		return q.kind == "chat_member" && c.chatID == q.chatID && c.userID == q.userID
	case "chat_administrators":
		return admin && c.chatID == q.chatID &&
			(q.kind == "chat_administrators" || q.kind == "chat_member")
	case "chat":
		return c.chatID == q.chatID &&
			(q.kind == "chat" || q.kind == "chat_administrators" || q.kind == "chat_member")
	case "all_chat_administrators":
		return q.kind == "all_chat_administrators" ||
			(admin && (q.kind == "chat_administrators" || q.kind == "chat_member") && isGroupChat(chatType))
	case "all_private_chats":
		return q.kind == "all_private_chats" ||
			((q.kind == "chat" || q.kind == "chat_administrators" || q.kind == "chat_member") && !isGroupChat(chatType))
	case "all_group_chats":
		return q.kind == "all_group_chats" ||
			((q.kind == "chat" || q.kind == "chat_administrators" || q.kind == "chat_member") && isGroupChat(chatType))
	}
	return false
}

// wire is the BotCommandScope a registered list reports.
func (c commandScope) wire() botapi.BotCommandScope {
	return botapi.BotCommandScope{Type: c.kind, ChatID: c.chatID, UserID: c.userID}
}

// commandScopeTypes are the BotCommandScope types the Bot API defines.
var commandScopeTypes = map[string]bool{
	"default":                 true,
	"all_private_chats":       true,
	"all_group_chats":         true,
	"all_chat_administrators": true,
	"chat":                    true,
	"chat_administrators":     true,
	"chat_member":             true,
}

// parseCommandScope reads the scope of a commands call. An absent scope and
// the literal null are the default scope. It returns the scope and the
// description of the refusal, if any.
func parseCommandScope(raw string) (commandScope, string) {
	if raw = strings.TrimSpace(raw); raw == "" || raw == "null" {
		return defaultCommandScope, ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return defaultCommandScope, "Bad Request: can't parse scope JSON object"
	}
	// The fields are read one by one: Telegram takes chat_id as an integer or
	// a username, so the whole scope cannot be unmarshalled into a struct.
	kind := ""
	if raw, ok := fields["type"]; ok {
		if json.Unmarshal(raw, &kind) != nil {
			return defaultCommandScope, "Bad Request: unsupported command scope type"
		}
	}
	if !commandScopeTypes[kind] {
		return defaultCommandScope, "Bad Request: unsupported command scope type"
	}
	scope := commandScope{kind: kind}
	if raw, ok := fields["user_id"]; ok {
		if json.Unmarshal(raw, &scope.userID) != nil {
			return defaultCommandScope, "Bad Request: invalid user_id specified"
		}
	}
	switch kind {
	case "chat", "chat_administrators", "chat_member":
		id, ok := scopeChatID(fields)
		if !ok {
			return defaultCommandScope, "Bad Request: Invalid chat_id specified"
		}
		scope.chatID = id
		if kind == "chat_member" && scope.userID == 0 {
			return defaultCommandScope, "Bad Request: invalid user_id specified"
		}
	}
	return scope, ""
}

// scopeChatID reads the chat_id of a chat scope. Telegram takes an integer or
// a username; the stand resolves chats by integer id only, so a username is
// refused the way setChatMenuButton refuses a chat_id it cannot use.
func scopeChatID(fields map[string]json.RawMessage) (int64, bool) {
	raw, ok := fields["chat_id"]
	if !ok {
		return 0, false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text = strings.TrimSpace(text); text == "" {
			return 0, false
		}
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil || id == 0 {
			return 0, false
		}
		return id, true
	}
	var id int64
	if json.Unmarshal(raw, &id) != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func (s *Server) setMyCommands(w http.ResponseWriter, method string, params url.Values) {
	var cmds []botapi.BotCommand
	if err := json.Unmarshal([]byte(params.Get("commands")), &cmds); err != nil {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse commands JSON object", 0)
		return
	}
	scope, problem := parseCommandScope(params.Get("scope"))
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.mu.Lock()
	if s.commandLists == nil {
		s.commandLists = map[commandKey][]botapi.BotCommand{}
	}
	s.commandLists[commandKey{scope: scope, lang: params.Get("language_code")}] = cmds
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) getMyCommands(w http.ResponseWriter, method string, params url.Values) {
	scope, problem := parseCommandScope(params.Get("scope"))
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.writeResult(w, method, params, s.CommandsFor(scope.wire(), params.Get("language_code")))
}

func (s *Server) deleteMyCommands(w http.ResponseWriter, method string, params url.Values) {
	scope, problem := parseCommandScope(params.Get("scope"))
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.mu.Lock()
	delete(s.commandLists, commandKey{scope: scope, lang: params.Get("language_code")})
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// Commands returns the list the bot registered for the default scope and no
// language: what a bot with no scoped lists shows everyone, and what
// getMyCommands answers with no scope.
func (s *Server) Commands() []botapi.BotCommand {
	return s.CommandsFor(defaultCommandScope.wire(), "")
}

// CommandsFor returns the commands a user of the scope would see, speaking
// languageCode, walking the chain the Bot API documents: the first list that
// is set wins, a narrower scope before a wider one and the language's own list
// before the one with no language.
func (s *Server) CommandsFor(scope botapi.BotCommandScope, languageCode string) []botapi.BotCommand {
	query, problem := parseCommandScope(scopeJSON(scope))
	if problem != "" {
		return []botapi.BotCommand{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commandsForLocked(query, languageCode)
}

// commandsForLocked resolves the chain for one query. Caller holds s.mu.
func (s *Server) commandsForLocked(q commandScope, languageCode string) []botapi.BotCommand {
	chatType := ""
	if q.chatID != 0 {
		chatType = s.chatTypeLocked(q.chatID)
	}
	admin := s.queryIsAdminLocked(q)
	found := false
	best, bestSpecificity, bestLang := []botapi.BotCommand(nil), 0, 0
	for key, list := range s.commandLists {
		if !key.scope.covers(q, chatType, admin) {
			continue
		}
		// A list registered for another language is not the answer for this
		// one: it neither wins nor blocks a wider scope's list.
		if key.lang != languageCode && key.lang != "" {
			continue
		}
		lang := 1
		if key.lang == languageCode {
			lang = 2
		}
		if specific := key.scope.specificity(); !found || specific > bestSpecificity ||
			(specific == bestSpecificity && lang > bestLang) {
			best, bestSpecificity, bestLang, found = list, specific, lang, true
		}
	}
	if !found {
		return []botapi.BotCommand{}
	}
	return append([]botapi.BotCommand{}, best...)
}

// queryIsAdminLocked reports whether the chain of the query goes through the
// administrator scopes: it does for an administrator query, and for a
// chat_member query naming a person who is an administrator of that chat.
// Caller holds s.mu.
func (s *Server) queryIsAdminLocked(q commandScope) bool {
	switch q.kind {
	case "chat_administrators", "all_chat_administrators":
		return true
	case "chat_member":
		c := s.chats[q.chatID]
		if c == nil {
			return false
		}
		member := c.members[q.userID]
		if member == nil {
			return false
		}
		status := stringOf(member["status"])
		return status == "creator" || status == "administrator"
	}
	return false
}

// CommandList is one registered list of commands, as /sim/state reports them.
type CommandList struct {
	Scope        botapi.BotCommandScope `json:"scope"`
	LanguageCode string                 `json:"language_code,omitempty"`
	Commands     []botapi.BotCommand    `json:"commands"`
}

// CommandLists returns every list the bot registered, in a stable order.
func (s *Server) CommandLists() []CommandList {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CommandList, 0, len(s.commandLists))
	for key, list := range s.commandLists {
		out = append(out, CommandList{
			Scope:        key.scope.wire(),
			LanguageCode: key.lang,
			Commands:     append([]botapi.BotCommand{}, list...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Scope.Type != b.Scope.Type {
			return a.Scope.Type < b.Scope.Type
		}
		if a.Scope.ChatID != b.Scope.ChatID {
			return a.Scope.ChatID < b.Scope.ChatID
		}
		if a.Scope.UserID != b.Scope.UserID {
			return a.Scope.UserID < b.Scope.UserID
		}
		return a.LanguageCode < b.LanguageCode
	})
	return out
}

// scopeJSON renders a scope the way a bot sends it, so a lookup reuses the
// parser the methods use.
func scopeJSON(scope botapi.BotCommandScope) string {
	raw, err := json.Marshal(scope)
	if err != nil {
		return `{"type":"default"}`
	}
	return string(raw)
}
