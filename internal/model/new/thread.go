package new

import (
	"time"

	"github.com/gofrs/uuid/v5"
)

// ThreadKind maps to im_thread.thread.kind
type ThreadKind int

const (
	ThreadUnspecified ThreadKind = iota
	ThreadDirect
	ThreadGroup
	ThreadChannel
)

// Thread maps to im_thread.thread
type Thread struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	DomainID    int        `json:"domain_id" db:"domain_id"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	Kind        ThreadKind `json:"kind" db:"kind"`
	Owner       uuid.UUID  `json:"owner" db:"owner"`
	Subject     string     `json:"subject" db:"subject"`
	Description string     `json:"description" db:"description"`
}

// ThreadMember is the JSONB shape stored in im_thread.thread.members
type ThreadMember struct {
	ID       uuid.UUID `json:"id" db:"id"`
	MemberID uuid.UUID `json:"member_id" db:"member_id"`
	Role     int       `json:"role" db:"role"`
}
