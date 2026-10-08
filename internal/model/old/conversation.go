package old

import (
	"time"

	"github.com/google/uuid"
)

type GroupedConversation struct {
	ConvIDs       uuid.UUIDs          `db:"conv_ids"`
	Initiator     int                 `db:"initiator"`
	ClientType    string              `db:"client_type"`
	Title         string              `db:"title"`
	DomainID      int                 `db:"domain_id"`
	CreatedAt     time.Time           `db:"created_at"`
	InternalUsers []*ConversationUser `db:"internal_users"`
}

type ConversationUser struct {
	ChannelIDs  []uuid.UUID `json:"channel_ids"`
	UserID      int         `json:"user_id"`
	CreatedAt   time.Time   `json:"created_at"`
	ClosedAt    time.Time   `json:"closed_at"`
	LeaveReason *string     `json:"leave_reason"`
	Name        *string     `json:"name"`
}
