package new

import (
	"time"

	"github.com/gofrs/uuid/v5"
)

type BaseModel struct {
	ID       uuid.UUID `json:"id" db:"id"`
	DomainID int       `json:"domain_id" db:"domain_id"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
