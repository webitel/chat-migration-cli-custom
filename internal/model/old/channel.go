package old

import (
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
)

type Channel struct {
	ID             uuid.UUID  `db:"id"`
	Type           string     `db:"type"`
	ConversationID uuid.UUID  `db:"conversation_id"`
	UserID         int64      `db:"user_id"`
	Connection     string     `db:"connection"`
	CreatedAt      time.Time  `db:"created_at"`
	Internal       bool       `db:"internal"`
	ClosedAt       *time.Time `db:"closed_at"`
	DomainID       int64      `db:"domain_id"`
	FlowBridge     bool       `db:"flow_bridge"`
	UpdatedAt      *time.Time `db:"updated_at"`
	Name           *string    `db:"name"`
	JoinedAt       *time.Time `db:"joined_at"`
	ClosedCause    *string    `db:"closed_cause"`
	Host           *string    `db:"host"`
	Props          []byte     `db:"props"`
	PublicName     *string    `db:"public_name"`
}
