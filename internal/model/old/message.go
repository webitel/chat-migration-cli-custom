package old

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid/v5"
)

type OldMessageType string

const (
	MessageText   OldMessageType = "text"
	MessageFile   OldMessageType = "file"
	MessageEdit   OldMessageType = "edit"
	MessageRead   OldMessageType = "read"
	MessageInvite OldMessageType = "invite"
	MessageJoined OldMessageType = "joined"
	MessageClosed OldMessageType = "closed"
	MessageTyping OldMessageType = "typing"
	MessageUpload OldMessageType = "upload"
)

type Message struct {
	ID             int64          `db:"id"`
	UserID         *int           `db:"user_id"`
	Internal       *bool          `db:"internal"`
	Text           *string        `db:"text"`
	ChannelID      *uuid.UUID     `db:"channel_id"`
	ConversationID uuid.UUID      `db:"conversation_id"`
	Variables      []byte         `db:"variables"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      *time.Time     `db:"updated_at"`
	Type           OldMessageType `db:"type"`
	FileID         *int64         `db:"file_id"`
	FileSize       *int64         `db:"file_size"`
	FileType       *string        `db:"file_type"`
	FileName       *string        `db:"file_name"`
	FileURL        *string        `db:"file_url"`
	Content        *Content       `db:"content"`
}

// Content is the Go representation of the chat.message.content JSONB column.
// It is serialized/deserialized with standard encoding/json.
// JSON keys match protojson output (UseProtoNames=true, UseEnumNumbers=true).
type Content struct {
	Keyboard *ReplyMarkup `json:"keyboard,omitempty"`
}

// ReplyMarkup is the keyboard / quick-reply layout attached to a message.
// JSON key: "keyboard"
type ReplyMarkup struct {
	// Rows of buttons.
	Buttons []*ButtonRow `json:"buttons,omitempty"`
	// When true, free-form input is blocked; user must press a button.
	NoInput bool `json:"no_input,omitempty"`
}

// ButtonRow is one horizontal row of buttons.
type ButtonRow struct {
	Row []*Button `json:"row,omitempty"`
}

// Button is one interactive button inside a row.
// Exactly one of Url, Code, Share will be non-nil (mirrors proto oneof).
type Button struct {
	// Display caption shown to the user.
	Text string `json:"text,omitempty"`

	// oneof type -----------------------------------------------
	// Navigate to this URL.
	Url *string `json:"url,omitempty"`
	// Postback / callback data sent back when clicked.
	Code *string `json:"code,omitempty"`
	// Request to share contact info. Value is ButtonRequest enum (int32).
	// 0 = phone, 1 = email, 2 = contact, 3 = location
	Share *int32 `json:"share,omitempty"`
	// ----------------------------------------------------------
}

// ButtonRequest mirrors Button.Request proto enum.
type ButtonRequest int32

const (
	ButtonRequestPhone    ButtonRequest = 0
	ButtonRequestEmail    ButtonRequest = 1
	ButtonRequestContact  ButtonRequest = 2
	ButtonRequestLocation ButtonRequest = 3
)

// Postback represents a button-click event sent by the end-user.
// JSON key: "postback"
type Postback struct {
	// ID of the message that contained the button.
	Mid int64 `json:"mid,omitempty"`
	// Callback data that was bound to the button (Button.code).
	Code string `json:"code,omitempty"`
	// Display caption of the button that was clicked.
	Text string `json:"text,omitempty"`
}

// Account is the contact/sender info stored alongside a message.
// Mirrors webitel.chat.server.Account.
// JSON key: "contact"
type Account struct {
	ID        int64  `json:"id,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Contact   string `json:"contact,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// ── sql.Scanner / driver.Valuer ──────────────────────────────────────────────

// Scan implements sql.Scanner so *Content can be used directly in sqlx scans.
func (c *Content) Scan(src interface{}) error {
	if src == nil {
		return nil // NULL column → leave zero value
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("content: cannot scan type %T", src)
	}
	return json.Unmarshal(b, c)
}

// Value implements driver.Valuer so Content can be written back to DB.
func (c Content) Value() (driver.Value, error) {
	if c.Keyboard == nil {
		return nil, nil // store NULL when empty
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
