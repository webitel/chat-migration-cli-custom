package olddb

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

type AppStore struct {
	db *DB
}

func NewAppStore(db *DB) *AppStore {
	return &AppStore{db: db}
}

func (s *AppStore) Get(ctx context.Context, offset, limit int) ([]*old.PortalApp, error) {
	query := `SELECT
    id,
    dc,
    app,
    token,
    expires_at,
    service_id,
    ua::text[],
    net::text[],
    web::text[],
    issuers::text[],
    grant_types::text[],
    response_types::text[],
    updated_at,
    updated_by,
    created_at,
    created_by,
    push,
    jwks_uri,
    jwks,
    jwt_identity,
    "limit"
FROM portal.service_app
ORDER BY id`

	if offset < 0 {
		offset = 0
	}

	if limit < 1 {
		limit = 1
	}

	query += ` OFFSET $1 LIMIT $2`

	rows, err := s.db.Pool().Query(ctx, query, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[old.PortalApp])
	if err != nil {
		return nil, err
	}

	return res, nil
}
