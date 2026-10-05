package new

import (
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
)

// ThreadRole maps to im_thread.thread_dialog.thread_role
type ThreadRole int

const (
	UnspecifiedRole ThreadRole = iota
	RoleMember
	RoleAdmin
	RoleSupervisor
	RoleOwner
)

// ThreadDialog maps to im_thread.thread_dialog
type ThreadDialog struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	DomainID    int        `json:"domain_id" db:"domain_id"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at" db:"deleted_at"`
	MemberID    uuid.UUID  `json:"member_id" db:"member_id"`
	ThreadID    uuid.UUID  `json:"thread_id" db:"thread_id"`
	ThreadRole  ThreadRole `json:"thread_role" db:"thread_role"`
	InvitedBy   *uuid.UUID `json:"invited_by" db:"invited_by"`
	LeaveReason *string    `json:"leave_reason" db:"leave_reason"`
}
