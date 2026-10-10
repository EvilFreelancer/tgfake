// Package botapi holds the wire shapes of the Telegram Bot API objects the
// tgfake server sends and reads. They are written here rather than borrowed
// from a Telegram library so that the server stays free of the library's
// opinions and dependencies: any client that speaks the Bot API can point at
// it, and a test can decode what the server answers with these types.
package botapi

// User mirrors the Bot API User object.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Chat mirrors the Bot API Chat object: type is private, group, supergroup
// or channel.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// MessageEntity marks a span of a message: a bot_command at offset 0 is what
// makes a text a command for every Bot API library.
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// InlineKeyboardButton is one button of an inline keyboard: exactly one of
// CallbackData, URL and WebApp says what a tap does.
type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

// WebAppInfo names the Mini App a button or the menu button opens.
type WebAppInfo struct {
	URL string `json:"url"`
}

// MenuButton mirrors the Bot API MenuButton: "commands" (the list of the
// bot's commands, what a bot that set nothing shows), "web_app" (a button
// that opens a Mini App) or "default" (no value of its own: the chat follows
// the bot's button, and the bot's button goes back to commands).
type MenuButton struct {
	Type   string      `json:"type"`
	Text   string      `json:"text,omitempty"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

// InlineKeyboardMarkup is the reply_markup a bot attaches to its menus.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// PhotoSize is one size of a photo a message carries.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

// Document is a file a message carries as a document.
type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

// Message mirrors the subset of the Bot API Message object a bot reads
// and writes.
type Message struct {
	MessageID      int                   `json:"message_id"`
	From           *User                 `json:"from,omitempty"`
	Chat           Chat                  `json:"chat"`
	Date           int64                 `json:"date"`
	EditDate       int64                 `json:"edit_date,omitempty"`
	Text           string                `json:"text,omitempty"`
	Caption        string                `json:"caption,omitempty"`
	Photo          []PhotoSize           `json:"photo,omitempty"`
	Document       *Document             `json:"document,omitempty"`
	Entities       []MessageEntity       `json:"entities,omitempty"`
	ReplyToMessage *Message              `json:"reply_to_message,omitempty"`
	ReplyMarkup    *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// CallbackQuery is what a tap on an inline button delivers.
type CallbackQuery struct {
	ID           string   `json:"id"`
	From         User     `json:"from"`
	Message      *Message `json:"message,omitempty"`
	ChatInstance string   `json:"chat_instance"`
	Data         string   `json:"data,omitempty"`
}

// MessageGenerationStopped is what a tap on the Stop button of a streamed
// draft delivers (Bot API 10.3): the chat, the topic and the draft. It names
// no user; the chat is the person's private chat.
type MessageGenerationStopped struct {
	Chat            Chat  `json:"chat"`
	MessageThreadID int   `json:"message_thread_id,omitempty"`
	DraftID         int64 `json:"draft_id"`
}

// Update is one item of a getUpdates answer. The fake produces messages,
// callback queries and stopped drafts, the kinds the stand delivers.
type Update struct {
	UpdateID                 int                       `json:"update_id"`
	Message                  *Message                  `json:"message,omitempty"`
	CallbackQuery            *CallbackQuery            `json:"callback_query,omitempty"`
	StoppedMessageGeneration *MessageGenerationStopped `json:"stopped_message_generation,omitempty"`
}

// BotCommand is one entry of setMyCommands.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}
