package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var permissionFields = []string{
	"can_send_messages", "can_send_audios", "can_send_documents", "can_send_photos",
	"can_send_videos", "can_send_video_notes", "can_send_voice_notes", "can_send_polls",
	"can_send_other_messages", "can_add_web_page_previews", "can_change_info",
	"can_invite_users", "can_pin_messages", "can_manage_topics",
	"can_edit_tag",
}

var administratorFields = []string{
	"is_anonymous", "can_manage_chat", "can_delete_messages", "can_manage_video_chats",
	"can_restrict_members", "can_promote_members", "can_change_info", "can_invite_users",
	"can_post_messages", "can_edit_messages", "can_pin_messages", "can_post_stories",
	"can_edit_stories", "can_delete_stories", "can_manage_topics", "can_manage_direct_messages",
}

func boolFields(params url.Values, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		out[field] = truthy(params.Get(field))
	}
	return out
}

func permissionsOf(params url.Values) (map[string]any, bool) {
	var permissions map[string]any
	if raw := strings.TrimSpace(params.Get("permissions")); raw != "" {
		if json.Unmarshal([]byte(raw), &permissions) != nil || permissions == nil {
			return nil, false
		}
	} else {
		permissions = boolFields(params, permissionFields)
	}
	for _, field := range permissionFields {
		permissions[field] = truthyValue(permissions[field])
	}
	return permissions, true
}

func truthyValue(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case string:
		return truthy(value)
	default:
		return false
	}
}

// expandPermissions applies Telegram's legacy dependent-permission rules.
func expandPermissions(p map[string]any, independent bool) {
	if independent {
		return
	}
	if p["can_send_messages"] == true {
		for _, field := range []string{"can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos", "can_send_video_notes", "can_send_voice_notes"} {
			p[field] = true
		}
	}
	if p["can_send_other_messages"] == true {
		p["can_send_messages"] = true
		p["can_send_polls"] = true
		for _, field := range []string{"can_send_audios", "can_send_documents", "can_send_photos", "can_send_videos", "can_send_video_notes", "can_send_voice_notes"} {
			p[field] = true
		}
	}
	if p["can_add_web_page_previews"] == true {
		p["can_send_messages"] = true
	}
}

func allPermissions(p map[string]any) bool {
	for _, field := range permissionFields {
		// can_edit_tag controls whether a restricted member may edit their
		// member tag; Telegram does not require it when lifting restrictions.
		if field == "can_edit_tag" {
			continue
		}
		if p[field] != true {
			return false
		}
	}
	return true
}

func (s *Server) normalizedUntil(raw string) int64 {
	until, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	delta := time.Unix(until, 0).Sub(s.now())
	if until == 0 || delta < 30*time.Second || delta > 366*24*time.Hour {
		return 0
	}
	return until
}

func (s *Server) moderateChat(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	s.mu.Lock()
	chat := s.chats[chatID]
	if chat == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat not found", 0)
		return
	}
	lower := strings.ToLower(method)
	if (lower == "restrictchatmember" || lower == "promotechatmember" || lower == "setchatpermissions" || lower == "setchatadministratorcustomtitle") && chat.typ != "supergroup" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: method is available only for supergroups", 0)
		return
	}
	if problem := s.moderationRightsProblemLocked(chat, lower); problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	if lower == "setchatpermissions" {
		permissions, valid := permissionsOf(params)
		if !valid {
			s.mu.Unlock()
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse chat permissions JSON object", 0)
			return
		}
		expandPermissions(permissions, truthy(params.Get("use_independent_chat_permissions")))
		chat.info["permissions"] = permissions
		s.mu.Unlock()
		s.writeResult(w, method, params, true)
		return
	}
	userID := integerOf(params.Get("user_id"))
	if userID == s.opts.BotID {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: PARTICIPANT_ID_INVALID", 0)
		return
	}
	expireMemberLocked(chat, userID, s.now())
	member := chat.members[userID]
	if userID <= 0 || member == nil {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: user not found", 0)
		return
	}
	oldMember := cloneMap(member)
	if status := stringOf(member["status"]); status == "creator" && lower != "setchatadministratorcustomtitle" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't remove chat owner", 0)
		return
	} else if status == "administrator" && (lower == "restrictchatmember" || lower == "banchatmember") {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: user is an administrator of the chat", 0)
		return
	}
	switch lower {
	case "restrictchatmember":
		permissions, valid := permissionsOf(params)
		if !valid {
			s.mu.Unlock()
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse chat permissions JSON object", 0)
			return
		}
		expandPermissions(permissions, truthy(params.Get("use_independent_chat_permissions")))
		if allPermissions(permissions) {
			member = map[string]any{"status": "member", "user": member["user"]}
		} else {
			member = map[string]any{"status": "restricted", "user": member["user"], "is_member": true, "until_date": s.normalizedUntil(params.Get("until_date"))}
			for key, value := range permissions {
				member[key] = value
			}
		}
		chat.members[userID] = member
	case "banchatmember":
		chat.members[userID] = map[string]any{"status": "kicked", "user": member["user"], "until_date": s.normalizedUntil(params.Get("until_date"))}
	case "unbanchatmember":
		if !truthy(params.Get("only_if_banned")) || member["status"] == "kicked" {
			chat.members[userID] = map[string]any{"status": "left", "user": member["user"]}
		}
	case "promotechatmember":
		rights := boolFields(params, administratorFields)
		promoted := false
		for _, value := range rights {
			promoted = promoted || value == true
		}
		if !promoted {
			chat.members[userID] = map[string]any{"status": "member", "user": member["user"]}
		} else {
			rights["status"], rights["user"], rights["can_be_edited"] = "administrator", member["user"], true
			chat.members[userID] = rights
		}
	case "setchatadministratorcustomtitle":
		member["custom_title"] = params.Get("custom_title")
	}
	if lower != "setchatadministratorcustomtitle" {
		updated := s.memberUpdatedLocked(chat, *s.botUser(), oldMember, chat.members[userID], nil)
		s.pushMembershipUpdateLocked(userID, updated)
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) moderationRightsProblemLocked(chat *chatState, method string) string {
	bot := chat.members[s.opts.BotID]
	if bot == nil || (bot["status"] != "administrator" && bot["status"] != "creator") {
		return "Bad Request: CHAT_ADMIN_REQUIRED"
	}
	if bot["status"] == "creator" {
		return ""
	}
	switch method {
	case "restrictchatmember", "banchatmember", "unbanchatmember", "setchatpermissions":
		if bot["can_restrict_members"] != true {
			return "Bad Request: not enough rights to restrict/unrestrict chat member"
		}
	case "promotechatmember", "setchatadministratorcustomtitle":
		if bot["can_promote_members"] != true {
			return "Bad Request: not enough rights to promote/demote chat member"
		}
	}
	return ""
}

func expireMemberLocked(chat *chatState, userID int64, now time.Time) {
	member := chat.members[userID]
	if member == nil {
		return
	}
	until := integerOf(member["until_date"])
	if until <= 0 || now.Unix() < until {
		return
	}
	switch member["status"] {
	case "restricted":
		chat.members[userID] = map[string]any{"status": "member", "user": member["user"]}
	case "kicked":
		chat.members[userID] = map[string]any{"status": "left", "user": member["user"]}
	}
}

func expireMembersLocked(chat *chatState, now time.Time) {
	for userID := range chat.members {
		expireMemberLocked(chat, userID, now)
	}
}
