package new

import (
	"time"

	"github.com/gofrs/uuid/v5"
)

// ThreadPermission maps to im_thread.thread_permission
type ThreadPermission struct {
	ID                          uuid.UUID `json:"id" db:"id"`
	ThreadID                    uuid.UUID `json:"thread_id" db:"thread_id"`
	ThreadDialogID              uuid.UUID `json:"thread_dialog_id" db:"thread_dialog_id"`
	MemberID                    uuid.UUID `json:"member_id" db:"member_id"`
	CanSendMessages             bool      `json:"can_send_messages" db:"can_send_messages"`
	CanAddMembers               bool      `json:"can_add_members" db:"can_add_members"`
	CanRemoveMembers            bool      `json:"can_remove_members" db:"can_remove_members"`
	CanChangeMembersPermissions bool      `json:"can_change_members_permissions" db:"can_change_members_permissions"`
	CanChangeThreadInfo         bool      `json:"can_change_thread_info" db:"can_change_thread_info"`
	CreatedAt                   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at" db:"updated_at"`
}
