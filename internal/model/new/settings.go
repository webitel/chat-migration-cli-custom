package new

import (
	"time"

	"github.com/gofrs/uuid/v5"
)

type UserFilter int

const (
	All UserFilter = iota
	Nobody
	SameIssuer
)

type ContactSettings struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	ContactID        uuid.UUID  `json:"contact_id" db:"contact_id"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
	AllowInvitesFrom UserFilter `json:"allow_invites_from" db:"allow_invites_from"`
}
