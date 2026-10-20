package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// A bot keeps one list of commands per scope and per user language, as
// setMyCommands registers them. getMyCommands answers the list registered for
// exactly the scope and the language it names, and deleteMyCommands takes that
// one list away. What a user sees is another matter: the first list the chain
// of "Determining list of commands" reaches, a narrower scope before a wider
// one and the user's language before the list registered with none, which
// CommandsFor resolves.

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
		return q.kind == "all_group_chats" || q.kind == "all_chat_administrators" ||
			((q.kind == "chat" || q.kind == "chat_administrators" || q.kind == "chat_member") && isGroupChat(chatType))
	}
	return false
}

// wire is the BotCommandScope a registered list reports.
func (c commandScope) wire() botapi.BotCommandScope {
	return botapi.BotCommandScope{Type: c.kind, ChatID: c.chatID, UserID: c.userID}
}

// scopeProblem is the description of a scope the Bot API server cannot read:
// it prefixes the reason the way the server does.
func scopeProblem(reason string) string {
	return "Bad Request: can't parse BotCommandScope: " + reason
}

// parseCommandScope reads the scope of a commands call the way the Bot API
// server reads it: an absent scope is the default one, and every other value
// has to be a BotCommandScope object. It returns the scope, the chat_id a
// chat scope names (an integer or an @username, resolved by
// resolveScopeChatLocked) and the description of the refusal, if any.
func parseCommandScope(raw string) (commandScope, string, string) {
	if strings.TrimSpace(raw) == "" {
		return defaultCommandScope, "", ""
	}
	if !json.Valid([]byte(raw)) {
		return defaultCommandScope, "", "Bad Request: can't parse BotCommandScope JSON object"
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return defaultCommandScope, "", scopeProblem("BotCommandScope must be an Object")
	}
	kind, problem := jsonStringField(fields, "type")
	if problem != "" {
		return defaultCommandScope, "", scopeProblem(problem)
	}
	switch kind {
	case "default", "all_private_chats", "all_group_chats", "all_chat_administrators":
		return commandScope{kind: kind}, "", ""
	case "chat", "chat_administrators", "chat_member":
	default:
		return defaultCommandScope, "", scopeProblem("Unsupported type specified")
	}
	chat, problem := jsonStringField(fields, "chat_id")
	if problem != "" {
		return defaultCommandScope, "", scopeProblem(problem)
	}
	if chat == "" {
		return defaultCommandScope, "", scopeProblem("Empty chat_id specified")
	}
	scope := commandScope{kind: kind}
	if kind == "chat_member" {
		user, problem := jsonIntegerField(fields, "user_id")
		if problem != "" {
			return defaultCommandScope, "", scopeProblem(problem)
		}
		if user <= 0 {
			return defaultCommandScope, "", scopeProblem("Invalid user_id specified")
		}
		scope.userID = user
	}
	return scope, chat, ""
}

// jsonStringField reads a field the Bot API server takes as a string, which
// a number also is. It returns the reason the field cannot be read, if any.
func jsonStringField(fields map[string]json.RawMessage, name string) (string, string) {
	raw, ok := fields[name]
	if !ok {
		return "", `Can't find field "` + name + `"`
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && string(raw) != "null" {
		return text, ""
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && string(raw) != "null" {
		return number.String(), ""
	}
	return "", `Field "` + name + `" must be of type String`
}

// jsonIntegerField reads a field the Bot API server takes as an integer,
// given as a number or as a string of digits. It returns the reason the field
// cannot be read, if any.
func jsonIntegerField(fields map[string]json.RawMessage, name string) (int64, string) {
	text, problem := jsonStringField(fields, name)
	if problem != "" {
		if _, ok := fields[name]; ok {
			return 0, `Field "` + name + `" must be a Number`
		}
		return 0, problem
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, `Field "` + name + `" must be a valid Number`
	}
	return id, ""
}

// resolveScopeChatLocked fills in the chat a chat scope names, by integer id
// or by the @username of a configured group or channel, and refuses a scope
// Telegram refuses for that chat: the administrator and member scopes do not
// exist in a private chat, and a channel has no commands. A chat the stand
// cannot resolve is a chat Telegram does not find. Caller holds s.mu.
func (s *Server) resolveScopeChatLocked(scope *commandScope, chat string) string {
	if chat == "" {
		return ""
	}
	id, err := strconv.ParseInt(strings.TrimSpace(chat), 10, 64)
	if err != nil {
		var ok bool
		if id, ok = s.chatByUsernameLocked(chat); !ok {
			return "Bad Request: chat not found"
		}
	}
	if id == 0 {
		return "Bad Request: chat not found"
	}
	scope.chatID = id
	switch s.chatTypeLocked(id) {
	case "private":
		if scope.kind != "chat" {
			return "Bad Request: can't use specified scope in private chats"
		}
	case "channel":
		return "Bad Request: can't change commands in channel chats"
	}
	return ""
}

// languageCodeProblem refuses a language_code that is neither empty nor two
// lower-case Latin letters, as Telegram does for every commands call.
func languageCodeProblem(code string) string {
	if code == "" {
		return ""
	}
	if len(code) == 2 && 'a' <= code[0] && code[0] <= 'z' && 'a' <= code[1] && code[1] <= 'z' {
		return ""
	}
	return "Bad Request: invalid language code specified"
}

// commandKeyOf reads the scope and the language a commands call names, in
// the order Telegram checks them. It returns the key and the description of
// the refusal, if any.
func (s *Server) commandKeyOf(params url.Values) (commandKey, string) {
	scope, chat, problem := parseCommandScope(params.Get("scope"))
	if problem == "" {
		s.mu.Lock()
		problem = s.resolveScopeChatLocked(&scope, chat)
		s.mu.Unlock()
	}
	if problem == "" {
		problem = languageCodeProblem(params.Get("language_code"))
	}
	return commandKey{scope: scope, lang: params.Get("language_code")}, problem
}

// parseCommands reads the commands of setMyCommands the way the Bot API
// server reads them: nothing, or JSON null, is an empty list, and every item
// is an object with a command and a description. It returns the commands and
// the description of the refusal, if any.
func parseCommands(raw string) ([]botapi.BotCommand, string) {
	cmds := []botapi.BotCommand{}
	if raw == "" {
		return cmds, ""
	}
	if !json.Valid([]byte(raw)) {
		return nil, "Bad Request: can't parse commands JSON object"
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil, "Bad Request: expected an Array of BotCommand"
	}
	for _, item := range items {
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &fields) != nil || fields == nil {
			return nil, "Bad Request: can't parse BotCommand: expected an Object"
		}
		command, problem := jsonStringField(fields, "command")
		if problem != "" {
			return nil, "Bad Request: can't parse BotCommand: " + problem
		}
		description, problem := jsonStringField(fields, "description")
		if problem != "" {
			return nil, "Bad Request: can't parse BotCommand: " + problem
		}
		cmd := botapi.BotCommand{Command: command, Description: description}
		if raw, ok := fields["is_ephemeral"]; ok && json.Unmarshal(raw, &cmd.IsEphemeral) != nil {
			return nil, `Bad Request: can't parse BotCommand: Field "is_ephemeral" must be of type Boolean`
		}
		cmds = append(cmds, cmd)
	}
	return cmds, ""
}

// Command limits Telegram states for setMyCommands, in characters.
const (
	commandMax            = 32
	commandDescriptionMax = 256
)

// tidyCommands trims every command and description, drops a leading slash
// from a command and refuses an empty or overlong one, then a command with a
// character Telegram does not take, the way Telegram does before it stores the
// list. It returns the commands as Telegram keeps them.
func tidyCommands(cmds []botapi.BotCommand) ([]botapi.BotCommand, string) {
	out := make([]botapi.BotCommand, 0, len(cmds))
	for _, cmd := range cmds {
		cmd.Command = strings.TrimPrefix(strings.TrimSpace(cmd.Command), "/")
		cmd.Description = strings.TrimSpace(cmd.Description)
		switch {
		case cmd.Command == "":
			return nil, "Bad Request: command must be non-empty"
		case utf8.RuneCountInString(cmd.Command) > commandMax:
			return nil, "Bad Request: command length must not exceed " + strconv.Itoa(commandMax)
		case cmd.Description == "":
			return nil, "Bad Request: command description must be non-empty"
		case utf8.RuneCountInString(cmd.Description) > commandDescriptionMax:
			return nil, "Bad Request: command description length must not exceed " + strconv.Itoa(commandDescriptionMax)
		}
		out = append(out, cmd)
	}
	// Telegram itself then refuses a command with anything but lower-case
	// Latin letters, digits and underscores.
	for _, cmd := range out {
		for _, r := range cmd.Command {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				return nil, "Bad Request: BOT_COMMAND_INVALID"
			}
		}
	}
	return out, ""
}

func (s *Server) setMyCommands(w http.ResponseWriter, method string, params url.Values) {
	cmds, problem := parseCommands(params.Get("commands"))
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	key, problem := s.commandKeyOf(params)
	if problem == "" {
		cmds, problem = tidyCommands(cmds)
	}
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.mu.Lock()
	if s.commandLists == nil {
		s.commandLists = map[commandKey][]botapi.BotCommand{}
	}
	s.commandLists[key] = cmds
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) getMyCommands(w http.ResponseWriter, method string, params url.Values) {
	key, problem := s.commandKeyOf(params)
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	// The list registered for exactly this scope and language: getMyCommands
	// reads what setMyCommands wrote, it does not walk the chain a user's
	// client walks ("If commands aren't set, an empty list is returned").
	s.mu.Lock()
	list := append([]botapi.BotCommand{}, s.commandLists[key]...)
	s.mu.Unlock()
	s.writeResult(w, method, params, list)
}

func (s *Server) deleteMyCommands(w http.ResponseWriter, method string, params url.Values) {
	key, problem := s.commandKeyOf(params)
	if problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.mu.Lock()
	delete(s.commandLists, key)
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// Commands returns the list the bot registered for the default scope and no
// language: what a bot with no scoped lists shows everyone, and what
// getMyCommands answers with no scope and no language.
func (s *Server) Commands() []botapi.BotCommand {
	return s.CommandsFor(defaultCommandScope.wire(), "")
}

// CommandsFor returns the commands a user of the scope would see in the
// menu, speaking languageCode, walking the chain the Bot API documents under
// "Determining list of commands": the first list that is set wins, a narrower
// scope before a wider one and the language's own list before the one with no
// language. A scope with no Type is the default one.
func (s *Server) CommandsFor(scope botapi.BotCommandScope, languageCode string) []botapi.BotCommand {
	query := commandScope{kind: scope.Type, chatID: scope.ChatID, userID: scope.UserID}
	if query.kind == "" {
		query = defaultCommandScope
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
