package new

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
)

type MessageSystem struct {
	MessageID string         `json:"message_id" db:"message_id"`
	Type      string         `json:"type" db:"type"`
	Metadata  map[string]any `json:"metadata" db:"metadata"`
}

type MessageLocation struct {
	MessageID string  `json:"message_id" db:"message_id"`
	Address   *string `json:"address" db:"address"`
	Latitude  float64 `json:"latitude" db:"latitude"`
	Longitude float64 `json:"longitude" db:"longitude"`
	Name      *string `json:"name" db:"name"`
}

type MessageContact struct {
	MessageID   string  `json:"message_id" db:"message_id"`
	PhoneNumber *string `json:"phone_number" db:"phone_number"`
	Name        *string `json:"name" db:"name"`
	Email       *string `json:"email" db:"email"`
}

type MessageInteractive struct {
	Attachments *MessageContent `json:"attachments" db:"attachments"`
	SingleUse   bool            `json:"single_use" db:"single_use"`
	Kind        InteractiveKind `json:"kind" db:"kind"`
}

func (m *MessageInteractive) Value() (driver.Value, error) {
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	return payload, nil
}

func (m *MessageInteractive) Scan(value any) error {
	payload, ok := value.([]byte)
	if !ok {
		return errors.New("error scanning interactive message")
	}

	return json.Unmarshal(payload, m)
}

type InteractiveKind struct {
	Markup    *KeyboardButtonMarkup `json:"markup,omitempty"`
	ListReply *KeyboardListReply    `json:"list_reply,omitempty"`
}

type MessageContent struct {
	Images    bool `json:"images" db:"images"`
	Documents bool `json:"documents" db:"documents"`
}

const (
	ActionTypeNone     = "unknown"
	ActionTypeURL      = "url"
	ActionTypeCallback = "callback"
	ActionTypeRequest  = "request"
)

type KeyboardButtonMarkup struct {
	Rows []*KeyboardButtonRow `json:"rows"`
}

type KeyboardButtonRow struct {
	Buttons []*KeyboardButton `json:"buttons"`
}

type KeyboardButton struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Metadata map[string]any `json:"metadata"`
	Type     string         `json:"type"`
	URL      *string        `json:"url,omitempty"`
	Data     *string        `json:"data,omitempty"`
	Action   *string        `json:"action,omitempty"`
}

type ListReplySection struct {
	Section string            `json:"section"`
	Buttons []*KeyboardButton `json:"buttons"`
}

type KeyboardListReply struct {
	Title    string              `json:"title"`
	Sections []*ListReplySection `json:"sections"`
}

type InteractiveCallback struct {
	ReactedBy    uuid.UUID `json:"reacted_by" db:"reacted_by"`
	InReplyTo    uuid.UUID `json:"in_reply_to" db:"in_reply_to"`
	ButtonCode   string    `json:"button_code" db:"button_code"`
	CallbackData string    `json:"callback_data" db:"callback_data"`
	ReactedAt    time.Time `json:"reacted_at" db:"reacted_at"`
}
