package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
)

// ChatConfiguration sets the chat information and members a test controls.
// The bot starts as a member unless Members explicitly supplies its status.
type ChatConfiguration struct {
	Chat    botapi.ChatFullInfo `json:"chat"`
	Members []botapi.ChatMember `json:"members,omitempty"`
}

func cloneMap[T ~map[string]any](value T) T {
	data, _ := json.Marshal(value)
	var copied T
	_ = json.Unmarshal(data, &copied)
	return copied
}
func integerOf(value any) int64 { n, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64); return n }
func stringOf(value any) string {
	if value == nil {
		return ""
	}
	str, _ := value.(string)
	return str
}
func formatCoordinate(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

// ConfigureChat replaces a chat's configured metadata and membership without
// changing its transcript. Values are copied; it is safe for concurrent use.
func (s *Server) ConfigureChat(in ChatConfiguration) error {
	info := cloneMap(in.Chat)
	id := integerOf(info["id"])
	typ := stringOf(info["type"])
	if id == 0 || (typ != "private" && typ != "group" && typ != "supergroup" && typ != "channel") {
		return fmt.Errorf("chat requires a non-zero id and a Telegram chat type")
	}
	info["id"] = id
	members := map[int64]map[string]any{}
	for _, member := range in.Members {
		copied := cloneMap(member)
		user, ok := copied["user"].(map[string]any)
		if !ok || integerOf(user["id"]) <= 0 {
			return fmt.Errorf("member requires a user with a positive id")
		}
		status := stringOf(copied["status"])
		user["id"] = integerOf(user["id"])
		if user["first_name"] == nil {
			user["first_name"] = defaultFirstName
		}
		if user["is_bot"] == nil {
			user["is_bot"] = false
		}
		switch status {
		case "member", "left":
		case "creator":
			if copied["is_anonymous"] == nil {
				copied["is_anonymous"] = false
			}
		case "administrator":
			for _, field := range []string{"can_be_edited", "is_anonymous", "can_manage_chat", "can_delete_messages", "can_manage_video_chats", "can_restrict_members", "can_promote_members", "can_change_info", "can_invite_users", "can_post_stories", "can_edit_stories", "can_delete_stories"} {
				if copied[field] == nil {
					copied[field] = false
				}
			}
		case "restricted", "kicked":
			if status == "restricted" {
				for _, field := range []string{"is_member", "can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos", "can_send_video_notes", "can_send_voice_notes", "can_send_polls", "can_send_other_messages", "can_add_web_page_previews", "can_change_info", "can_invite_users", "can_pin_messages", "can_manage_topics"} {
					if copied[field] == nil {
						copied[field] = false
					}
				}
			}
			if copied["until_date"] == nil {
				copied["until_date"] = float64(0)
			}
		default:
			return fmt.Errorf("unsupported chat member status %q", status)
		}
		members[integerOf(user["id"])] = map[string]any(copied)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	chat := s.ensureChatLocked(id, typ, stringOf(info["title"]), nil)
	chat.typ, chat.title, chat.info = typ, stringOf(info["title"]), map[string]any(info)
	chat.members = members
	if chat.members[s.opts.BotID] == nil {
		chat.members[s.opts.BotID] = userMember(s.botUser())
	}
	return nil
}

func (s *Server) knownUserLocked(id int64) *botapi.User {
	if id == s.opts.BotID {
		return s.botUser()
	}
	for _, state := range s.inlineQueries {
		if state.query.From.ID == id {
			return copyPointer(&state.query.From)
		}
	}
	for _, query := range s.webQueries {
		if query.user.ID == id {
			return copyPointer(&query.user)
		}
	}
	for _, chat := range s.chats {
		if member := chat.members[id]; member != nil {
			raw, _ := json.Marshal(member["user"])
			var user botapi.User
			if json.Unmarshal(raw, &user) == nil {
				return &user
			}
		}
	}
	return nil
}

// UserProfileConfiguration sets profile photo sets referencing uploaded photos.
type UserProfileConfiguration struct {
	User   botapi.User          `json:"user"`
	Photos [][]botapi.PhotoSize `json:"photos"`
}

// ConfigureUserProfile copies the user and photo sets for getUserProfilePhotos.
func (s *Server) ConfigureUserProfile(in UserProfileConfiguration) error {
	if in.User.ID <= 0 {
		return fmt.Errorf("user requires a positive id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	photos := make([][]botapi.PhotoSize, 0, len(in.Photos))
	for _, sizes := range in.Photos {
		if len(sizes) == 0 {
			return fmt.Errorf("photo size array must not be empty")
		}
		copy := make([]botapi.PhotoSize, 0, len(sizes))
		for _, size := range sizes {
			file := s.files[size.FileID]
			if file == nil || file.kind != "photo" {
				return fmt.Errorf("profile photo %q is not an uploaded photo", size.FileID)
			}
			copy = append(copy, botapi.PhotoSize{FileID: file.id, FileUniqueID: "u" + file.id, Width: file.width, Height: file.height, FileSize: len(file.data)})
		}
		photos = append(photos, copy)
	}
	s.ensureChatLocked(in.User.ID, "private", "", copyPointer(&in.User))
	s.profiles[in.User.ID] = botapi.UserProfilePhotos{TotalCount: len(photos), Photos: photos}
	return nil
}

func (s *Server) simUserProfile(w http.ResponseWriter, req *http.Request) {
	var in UserProfileConfiguration
	if !readJSON(w, req, &in) {
		return
	}
	if err := s.ConfigureUserProfile(in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func userMember(user *botapi.User) map[string]any {
	raw, _ := json.Marshal(user)
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	return map[string]any{"status": "member", "user": wire}
}

func (s *Server) chatInformation(w http.ResponseWriter, method string, params url.Values) {
	s.mu.Lock()
	var result any
	problem := ""
	if strings.EqualFold(method, "getUserProfilePhotos") {
		userID := integerOf(params.Get("user_id"))
		_, hasProfile := s.profiles[userID]
		known := hasProfile || s.knownUserLocked(userID) != nil
		if userID <= 0 {
			problem = "Bad Request: invalid user_id specified"
		} else if !known {
			problem = "Bad Request: user not found"
		} else {
			profile := s.profiles[userID]
			offset, limit := max(0, atoi(params.Get("offset"))), atoi(params.Get("limit"))
			if limit <= 0 || limit > 100 {
				limit = 100
			}
			from := min(offset, len(profile.Photos))
			to := min(from+limit, len(profile.Photos))
			photos := [][]botapi.PhotoSize{}
			for _, sizes := range profile.Photos[from:to] {
				photos = append(photos, append([]botapi.PhotoSize(nil), sizes...))
			}
			result = botapi.UserProfilePhotos{TotalCount: len(profile.Photos), Photos: photos}
		}
	} else {
		id, ok := chatIDOf(params)
		if !ok && strings.HasPrefix(params.Get("chat_id"), "@") {
			for _, chat := range s.chats {
				if chat.typ != "private" && strings.EqualFold(stringOf(chat.info["username"]), strings.TrimPrefix(params.Get("chat_id"), "@")) {
					id, ok = chat.id, true
					break
				}
			}
		}
		chat := s.chats[id]
		if !ok || chat == nil {
			problem = "Bad Request: chat not found"
		} else {
			switch strings.ToLower(method) {
			case "getchat":
				raw, _ := json.Marshal(chat.wire())
				var info map[string]any
				_ = json.Unmarshal(raw, &info)
				for key, value := range chat.info {
					info[key] = value
				}
				if info["accent_color_id"] == nil {
					info["accent_color_id"] = 0
				}
				if info["max_reaction_count"] == nil {
					info["max_reaction_count"] = 1
				}
				result = cloneMap(info)
			case "getchatmember":
				userID := integerOf(params.Get("user_id"))
				if userID <= 0 {
					problem = "Bad Request: invalid user_id specified"
				} else if member := chat.members[userID]; member != nil {
					result = cloneMap(member)
				} else if user := s.knownUserLocked(userID); user != nil {
					result = userMember(user)
					result.(map[string]any)["status"] = "left"
				} else {
					problem = "Bad Request: user not found"
				}
			case "getchatmembercount":
				count := 0
				for _, member := range chat.members {
					if member["status"] != "left" && member["status"] != "kicked" && (member["status"] != "restricted" || member["is_member"] != false) {
						count++
					}
				}
				result = count
			case "getchatadministrators":
				ids := make([]int64, 0, len(chat.members))
				for id := range chat.members {
					ids = append(ids, id)
				}
				sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
				admins := []map[string]any{}
				for _, id := range ids {
					member := chat.members[id]
					if member["status"] == "creator" || member["status"] == "administrator" {
						admins = append(admins, cloneMap(member))
					}
				}
				result = admins
			}
		}
	}
	s.mu.Unlock()
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	s.writeResult(w, method, params, result)
}

func (s *Server) simConfigureChat(w http.ResponseWriter, req *http.Request) {
	var in ChatConfiguration
	if !readJSON(w, req, &in) {
		return
	}
	if err := s.ConfigureChat(in); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
