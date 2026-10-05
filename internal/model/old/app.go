package old

import (
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
)

type PortalApp struct {
	ID            uuid.UUID  `db:"id"`
	DomainID      int        `db:"dc"`
	Name          string     `db:"app"`
	Token         string     `db:"token"`
	ExpiresAt     *time.Time `db:"expires_at"`
	ServiceID     uuid.UUID  `db:"service_id"`
	UA            []string   `db:"ua"`
	Net           []string   `db:"net"`
	Web           []string   `db:"web"`
	Issuers       []string   `db:"issuers"`
	GrantTypes    []string   `db:"grant_types"`
	ResponseTypes []string   `db:"response_types"`
	UpdatedAt     time.Time  `db:"updated_at"`
	UpdatedBy     *int64     `db:"updated_by"`
	CreatedAt     time.Time  `db:"created_at"`
	CreatedBy     *int64     `db:"created_by"`
	Push          []byte     `db:"push"`
	JwksURI       *string    `db:"jwks_uri"`
	Jwks          []byte     `db:"jwks"`
	JwtIdentity   []byte     `db:"jwt_identity"`
	Limit         []byte     `db:"limit"`
}
