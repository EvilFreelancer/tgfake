package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
	"github.com/EvilFreelancer/tgfake/pkg/webapp"
)

// Mini Apps, as Telegram handles them around a bot: the web_app buttons and
// the menu button a bot sets, and the launch a tap on either starts - the
// address of the app with the launch parameters in its fragment, and the
// launch data signed with the bot's token, so that whatever checks it on the
// app's side (core.telegram.org/bots/webapps, "Validating data received via
// the Mini App") gets the answer it would get from Telegram.

// webAppURLOf returns the address a web_app button opens, "" for any other
// button.
func webAppURLOf(b botapi.InlineKeyboardButton) string {
	if b.WebApp == nil {
		return ""
	}
	return b.WebApp.URL
}

// chatTypeLocked is the type of chat id: the one it has, or the one the
// stand gives a chat it has not seen (a negative id is a group). It creates
// nothing. Caller holds s.mu.
func (s *Server) chatTypeLocked(id int64) string {
	if c := s.chats[id]; c != nil {
		return c.typ
	}
	if id < 0 {
		return "group"
	}
	return "private"
}

// Token is the bot token the stand signs launch data with: the one it was
// started with, else the one in the path of the latest Bot API call.
func (s *Server) Token() string {
	if s.opts.Token != "" {
		return s.opts.Token
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// MenuButton is the menu button chat shows: its own, else the bot's; chat 0
// asks for the bot's. A bot that set nothing shows its commands.
func (s *Server) MenuButton(chat int64) botapi.MenuButton {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.menuButtonLocked(chat)
}

func (s *Server) menuButtonLocked(chat int64) botapi.MenuButton {
	// Telegram shows a bot's menu button in private chats only.
	if chat != 0 && s.chatTypeLocked(chat) != "private" {
		return botapi.MenuButton{Type: "commands"}
	}
	if b := s.chatMenus[chat]; chat != 0 && b != nil {
		return cloneMenuButton(*b)
	}
	if s.defaultMenu != nil {
		return cloneMenuButton(*s.defaultMenu)
	}
	return botapi.MenuButton{Type: "commands"}
}

func cloneMenuButton(b botapi.MenuButton) botapi.MenuButton {
	if b.WebApp != nil {
		app := *b.WebApp
		b.WebApp = &app
	}
	return b
}

// menuButtonChat reads the chat_id of a menu button method the way the Bot
// API does: as a user, so anything but a positive id (a group's included) is
// refused. Without chat_id the method is about the bot's own button, chat 0.
func (s *Server) menuButtonChat(w http.ResponseWriter, method string, params url.Values) (int64, bool) {
	raw := strings.TrimSpace(params.Get("chat_id"))
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: Invalid chat_id specified", 0)
		return 0, false
	}
	return id, true
}

// setChatMenuButton answers setChatMenuButton: a private chat's own button
// with chat_id, the bot's without. Type "default" (or no menu_button at all)
// removes the chat's own button, or puts the bot's back to its commands. The
// button is checked before the chat, as the Bot API does.
func (s *Server) setChatMenuButton(w http.ResponseWriter, method string, params url.Values) {
	button := botapi.MenuButton{Type: "default"}
	if raw := strings.TrimSpace(params.Get("menu_button")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &button); err != nil {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse menu button JSON object", 0)
			return
		}
	}
	switch button.Type {
	case "commands", "default":
		button = botapi.MenuButton{Type: button.Type}
	case "web_app":
		if strings.TrimSpace(button.Text) == "" {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: menu button text is empty", 0)
			return
		}
		if button.WebApp == nil {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: menu button Web App URL '' is invalid", 0)
			return
		}
		if problem := webapp.URLProblem(button.WebApp.URL); problem != "" {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: menu button "+problem, 0)
			return
		}
		button = cloneMenuButton(button)
	default:
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: unsupported menu button type", 0)
		return
	}
	chat, ok := s.menuButtonChat(w, method, params)
	if !ok {
		return
	}
	s.mu.Lock()
	switch {
	case chat != 0 && button.Type == "default":
		delete(s.chatMenus, chat)
	case chat != 0:
		s.chatMenus[chat] = &button
	case button.Type == "default":
		s.defaultMenu = nil
	default:
		s.defaultMenu = &button
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// getChatMenuButton answers getChatMenuButton with the button the chat shows.
func (s *Server) getChatMenuButton(w http.ResponseWriter, method string, params url.Values) {
	chat, ok := s.menuButtonChat(w, method, params)
	if !ok {
		return
	}
	s.writeResult(w, method, params, s.MenuButton(chat))
}

// WebAppLaunch is a person opening a Mini App: from a web_app button (URL
// set) or from the menu button of the chat (URL empty). Zero fields take the
// defaults of a message: user 4242 "alice".
type WebAppLaunch struct {
	ChatID      int64  `json:"chat_id,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	URL         string `json:"url,omitempty"`
	StartParam  string `json:"start_param,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"` // light or dark (default)
	Platform    string `json:"platform,omitempty"`
	Version     string `json:"version,omitempty"`
}

// LaunchedWebApp is what the client opens: the app's address with the launch
// parameters in its fragment, and the signed launch data among them.
type LaunchedWebApp struct {
	URL         string            `json:"url"`
	InitData    string            `json:"init_data"`
	ThemeParams map[string]string `json:"theme_params"`
	Version     string            `json:"version"`
	Platform    string            `json:"platform"`
}

// LaunchWebApp builds a launch the way Telegram does when a person opens a
// Mini App: the launch data (query_id, user, auth_date, start_param when
// there is one) signed with the bot's token, and the address of the app with
// tgWebAppData, tgWebAppVersion, tgWebAppPlatform and tgWebAppThemeParams in
// its fragment - after the fragment the address already has, behind a
// question mark, the form the official SDK parses - and a start parameter in
// its query as tgWebAppStartParam. The Ed25519 signature Telegram adds for
// third parties is left out: only Telegram holds that key.
func (s *Server) LaunchWebApp(req WebAppLaunch) (LaunchedWebApp, error) {
	token := s.Token()
	if token == "" {
		return LaunchedWebApp{}, errors.New("no bot token to sign with yet: let the bot call the stand first, or start it with --token")
	}
	app := strings.TrimSpace(req.URL)
	if app == "" {
		menu := s.MenuButton(req.ChatID)
		if menu.Type != "web_app" || menu.WebApp == nil {
			return LaunchedWebApp{}, errors.New("no Mini App to open: give a url, or let the bot set a web_app menu button")
		}
		app = menu.WebApp.URL
	}
	if problem := webapp.URLProblem(app); problem != "" {
		return LaunchedWebApp{}, errors.New(problem)
	}
	in := IncomingMessage{ChatID: req.ChatID, UserID: req.UserID, Username: req.Username, FirstName: req.FirstName, Text: "-"}
	in.normalize()
	user, _ := json.Marshal(map[string]any{
		"id": in.UserID, "first_name": in.FirstName, "username": in.Username,
		"language_code": "en", "allows_write_to_pm": true,
	})
	s.mu.Lock()
	s.nextQuery++
	query := fmt.Sprintf("tgfake-%d", s.nextQuery)
	authDate := s.now().Unix()
	s.mu.Unlock()

	data := url.Values{}
	data.Set("query_id", query)
	data.Set("user", string(user))
	data.Set("auth_date", strconv.FormatInt(authDate, 10))
	if req.StartParam != "" {
		data.Set("start_param", req.StartParam)
	}
	data.Set("hash", webapp.Sign(token, data))
	initData := data.Encode()

	scheme := strings.ToLower(strings.TrimSpace(req.ColorScheme))
	if scheme != "light" {
		scheme = webapp.DefaultColorScheme
	}
	theme := webapp.ThemeParams(scheme)
	themeJSON, _ := json.Marshal(theme)
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = webapp.Version
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = webapp.Platform
	}
	launch := url.Values{}
	launch.Set("tgWebAppData", initData)
	launch.Set("tgWebAppVersion", version)
	launch.Set("tgWebAppPlatform", platform)
	launch.Set("tgWebAppThemeParams", string(themeJSON))

	if req.StartParam != "" {
		u, err := url.Parse(app)
		if err != nil {
			return LaunchedWebApp{}, err
		}
		q := u.Query()
		q.Set("tgWebAppStartParam", req.StartParam)
		u.RawQuery = q.Encode()
		app = u.String()
	}
	return LaunchedWebApp{
		URL:         webapp.AppendLaunchParams(app, launch.Encode()),
		InitData:    initData,
		ThemeParams: theme,
		Version:     version,
		Platform:    platform,
	}, nil
}
