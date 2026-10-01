package old

import (
	"time"

	"github.com/google/uuid"
)

type Client struct {
	ID         int       `db:"id"`
	Name       string    `db:"name"`
	Number     *string   `db:"number"`
	CreatedAt  time.Time `db:"created_at"`
	ExternalID string    `db:"external_id"`
	FirstName  *string   `db:"first_name"`
	LastName   *string   `db:"last_name"`
	Type       string    `db:"type"`
	DomainIDs  []int     `db:"domain_ids"`
	Gateways   []int     `db:"gateways"`
}

type PortalClient struct {
	ID        int        `db:"id"`
	Name      string     `db:"name"`
	Number    *string    `db:"number"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
	ProfileID uuid.UUID  `db:"profile_id"`
	FirstName *string    `db:"first_name"`
	LastName  *string    `db:"last_name"`
	Type      string     `db:"type"`
	DomainID  int        `db:"dc"`
	Sub       string     `db:"sub"`
}
