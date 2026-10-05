package new

import (
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
)

// MessageType maps to im_message.messages.type
type MessageType int16

const (
	MessageTypeUnknown     MessageType = iota
	MessageTypeText                    // 1
	MessageTypeFile                    // 2
	MessageTypeImage                   // 3
	MessageTypeSystem                  // 4
	MessageTypeInteractive             // 5
	MessageTypeLocation                // 6
	MessageTypeContact                 // 7
)

// Message maps to im_message.messages
type Message struct {
	ID          uuid.UUID           `json:"id" db:"id"`
	DomainID    int32               `json:"domain_id" db:"domain_id"`
	ThreadID    uuid.UUID           `json:"thread_id" db:"thread_id"`
	SenderID    uuid.UUID           `json:"sender_id" db:"sender_id"`
	MemberID    uuid.UUID           `json:"member_id" db:"member_id"`
	Type        MessageType         `json:"type" db:"type"`
	Body        string              `json:"body" db:"body"`
	Metadata    []byte              `json:"metadata" db:"metadata"`
	Interactive *MessageInteractive `json:"interactive" db:"interactive"`
	CreatedAt   time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at" db:"updated_at"`
}

// MessageRead maps to im_message.message_reads
type MessageRead struct {
	DomainID  int32     `json:"domain_id" db:"domain_id"`
	ThreadID  uuid.UUID `json:"thread_id" db:"thread_id"`
	MessageID uuid.UUID `json:"message_id" db:"message_id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
}
