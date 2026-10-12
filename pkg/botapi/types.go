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
	Text                         string          `json:"text"`
	CallbackData                 string          `json:"callback_data,omitempty"`
	URL                          string          `json:"url,omitempty"`
	WebApp                       *WebAppInfo     `json:"web_app,omitempty"`
	SwitchInlineQuery            *string         `json:"switch_inline_query,omitempty"`
	SwitchInlineQueryCurrentChat *string         `json:"switch_inline_query_current_chat,omitempty"`
	CopyText                     *CopyTextButton `json:"copy_text,omitempty"`
}

// CopyTextButton copies text to the clipboard.
type CopyTextButton struct {
	Text string `json:"text"`
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

// File is the result of getFile; FilePath is relative to /file/bot<token>/.
type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int    `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

// Video is a video or animation, with the metadata supplied by the sender.
type Video struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Duration     int    `json:"duration"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

// Audio is an audio track or voice message.
type Audio struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	Performer    string `json:"performer,omitempty"`
	Title        string `json:"title,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

// VideoNote is a round video message.
type VideoNote struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Length       int    `json:"length"`
	Duration     int    `json:"duration"`
	FileSize     int    `json:"file_size,omitempty"`
}

// Location is a point on the map.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Contact is a person's phone number and name.
type Contact struct {
	PhoneNumber string `json:"phone_number"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	VCard       string `json:"vcard,omitempty"`
}

// Venue is a named location.
type Venue struct {
	Location        Location `json:"location"`
	Title           string   `json:"title"`
	Address         string   `json:"address"`
	FoursquareID    string   `json:"foursquare_id,omitempty"`
	FoursquareType  string   `json:"foursquare_type,omitempty"`
	GooglePlaceID   string   `json:"google_place_id,omitempty"`
	GooglePlaceType string   `json:"google_place_type,omitempty"`
}

// WebAppData is data a keyboard Mini App sends to the bot.
type WebAppData struct {
	Data       string `json:"data"`
	ButtonText string `json:"button_text"`
}

// InlineQuery is a user's request to an inline bot.
type InlineQuery struct {
	ID       string    `json:"id"`
	From     User      `json:"from"`
	Query    string    `json:"query"`
	Offset   string    `json:"offset"`
	ChatType string    `json:"chat_type,omitempty"`
	Location *Location `json:"location,omitempty"`
}

// ChosenInlineResult is a result selected by the user.
type ChosenInlineResult struct {
	ResultID        string `json:"result_id"`
	From            User   `json:"from"`
	Query           string `json:"query"`
	InlineMessageID string `json:"inline_message_id,omitempty"`
}

// SentWebAppMessage identifies an inline message sent by a Mini App.
type SentWebAppMessage struct {
	InlineMessageID string `json:"inline_message_id,omitempty"`
}

// PreparedInlineMessage is a stored Mini App message with an expiration date.
type PreparedInlineMessage struct {
	ID             string `json:"id"`
	ExpirationDate int64  `json:"expiration_date"`
}

// MessageID identifies a copied message.
type MessageID struct {
	MessageID int `json:"message_id"`
}

// ChatFullInfo and ChatMember carry the Telegram fields configured by a test.
// Their variants have different sets of optional and required properties.
type ChatFullInfo map[string]any
type ChatMember map[string]any

// UserProfilePhotos is the result of getUserProfilePhotos.
type UserProfilePhotos struct {
	TotalCount int           `json:"total_count"`
	Photos     [][]PhotoSize `json:"photos"`
}

// MessageOrigin describes the original sender of a forwarded message.
type MessageOrigin struct {
	Type       string `json:"type"`
	Date       int64  `json:"date"`
	SenderUser *User  `json:"sender_user,omitempty"`
	SenderChat *Chat  `json:"sender_chat,omitempty"`
	Chat       *Chat  `json:"chat,omitempty"`
	MessageID  int    `json:"message_id,omitempty"`
}

// Message mirrors the subset of the Bot API Message object a bot reads
// and writes.
type Message struct {
	MessageID           int                   `json:"message_id"`
	From                *User                 `json:"from,omitempty"`
	Chat                Chat                  `json:"chat"`
	Date                int64                 `json:"date"`
	EditDate            int64                 `json:"edit_date,omitempty"`
	Text                string                `json:"text,omitempty"`
	Caption             string                `json:"caption,omitempty"`
	Photo               []PhotoSize           `json:"photo,omitempty"`
	Document            *Document             `json:"document,omitempty"`
	Video               *Video                `json:"video,omitempty"`
	Animation           *Video                `json:"animation,omitempty"`
	Audio               *Audio                `json:"audio,omitempty"`
	Voice               *Audio                `json:"voice,omitempty"`
	VideoNote           *VideoNote            `json:"video_note,omitempty"`
	Location            *Location             `json:"location,omitempty"`
	Contact             *Contact              `json:"contact,omitempty"`
	Venue               *Venue                `json:"venue,omitempty"`
	WebAppData          *WebAppData           `json:"web_app_data,omitempty"`
	MediaGroupID        string                `json:"media_group_id,omitempty"`
	ForwardOrigin       *MessageOrigin        `json:"forward_origin,omitempty"`
	ViaBot              *User                 `json:"via_bot,omitempty"`
	HasProtectedContent bool                  `json:"has_protected_content,omitempty"`
	Entities            []MessageEntity       `json:"entities,omitempty"`
	CaptionEntities     []MessageEntity       `json:"caption_entities,omitempty"`
	ReplyToMessage      *Message              `json:"reply_to_message,omitempty"`
	ReplyMarkup         *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
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
// callback/inline queries, inline choices and stopped drafts.
type Update struct {
	UpdateID                 int                       `json:"update_id"`
	Message                  *Message                  `json:"message,omitempty"`
	CallbackQuery            *CallbackQuery            `json:"callback_query,omitempty"`
	StoppedMessageGeneration *MessageGenerationStopped `json:"stopped_message_generation,omitempty"`
	InlineQuery              *InlineQuery              `json:"inline_query,omitempty"`
	ChosenInlineResult       *ChosenInlineResult       `json:"chosen_inline_result,omitempty"`
}

// BotCommand is one entry of setMyCommands.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}
