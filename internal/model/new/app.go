package new

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid/v5"
)

// App does NOT embed BaseModel: im_account.app's shape (about/config/revoked_at,
// nullable updated_at) doesn't match it.
type App struct {
	ID        uuid.UUID  `db:"id"`
	DomainID  int        `db:"dc"`
	Name      string     `db:"name"`
	About     *string    `db:"about"`
	Config    []byte     `db:"config"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
	RevokedAt *time.Time `db:"revoked_at"`
}

// AppConfig mirrors im-account-service's Application proto field names and
// nesting exactly — im-account-service unmarshals config with DiscardUnknown:
// false, so a mismatched shape makes the row unreadable there.
type AppConfig struct {
	Clients *AppClients       `json:"clients,omitempty"`
	Service *AppServiceConfig `json:"service,omitempty"`
}

type AppClients struct {
	Ua  []string          `json:"ua,omitempty"`
	Net *ClientNet        `json:"net,omitempty"`
	Web *ClientWeb        `json:"web,omitempty"`
	Idp map[string]string `json:"idp,omitempty"`
	Jwt *JwtIdentity      `json:"jwt,omitempty"`
}

type ClientNet struct {
	Cidr []string `json:"cidr,omitempty"`
}

type ClientWeb struct {
	Origin []string `json:"origin,omitempty"`
}

type JwtIdentity struct {
	Enabled bool              `json:"enabled,omitempty"`
	JwksUri string            `json:"jwksUri,omitempty"`
	Jwks    []byte            `json:"jwks,omitempty"`
	Claims  map[string]string `json:"claims,omitempty"`
}

type AppServiceConfig struct {
	// Secret is always left empty: old service_app.token's compatibility with
	// this field is unconfirmed, so it is never carried over from the migration.
	Secret      string          `json:"secret,omitempty"`
	PushService json.RawMessage `json:"pushService,omitempty"`
	SendUpdate  json.RawMessage `json:"sendUpdate,omitempty"`
	RateLimits  json.RawMessage `json:"rateLimits,omitempty"`
}
