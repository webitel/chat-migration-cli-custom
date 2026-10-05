package new

import (
	"time"

	"github.com/gofrs/uuid/v5"
)

// DirectSettings maps to im_thread.direct_settings
type DirectSettings struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	DomainID       int        `json:"domain_id" db:"domain_id"`
	ThreadDialogID uuid.UUID  `json:"thread_dialog_id" db:"thread_dialog_id"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
	Title          string     `json:"title" db:"title"`
	MemberID       *uuid.UUID `json:"member_id" db:"member_id"`
}
