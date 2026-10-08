package olddb

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

type ClientStore struct {
	db *DB
}

func NewClientStore(db *DB) *ClientStore {
	return &ClientStore{db: db}
}

// GetFromDate returns the next page of clients ordered by id, keyset-paginated
// after afterID (i.e. c.id > afterID) rather than OFFSET-paginated, to avoid
// O(N) skip cost on large tables, restricted to the half-open window
// [from, to) on created_at: from <= created_at < to. types restricts the
// result to clients whose type is in that list (in addition to the
// unconditional exclusion of portal clients, which are migrated by a
// separate step).
func (s *ClientStore) GetFromDate(ctx context.Context, afterID, limit int, from, to time.Time, types []string) ([]*old.Client, error) {
	query := `SELECT
    id,
       name,
       number,
       created_at,
       external_id,
       first_name,
       last_name,
       COALESCE(type, 'webchat') type,
       channels.domains           domain_ids,
       channels.gateways              gateways
FROM chat.client c
         LEFT JOIN LATERAL (
    SELECT ARRAY_AGG(DISTINCT ch.domain_id) domains, ARRAY_AGG(DISTINCT ch.connection::bigint) gateways
    FROM chat.channel ch
    WHERE ch.user_id = c.id AND NOT ch.internal AND ch.connection IS NOT NULL
             ) channels ON true
WHERE channels.domains IS NOT NULL
AND type != 'portal'
AND type = ANY($5::text[])
AND c.created_at >= $3::timestamp
AND c.created_at < $4::timestamp
AND c.id > $1
ORDER BY c.id LIMIT $2`

	if afterID < 0 {
		afterID = 0
	}

	if limit < 1 {
		limit = 1
	}

	rows, err := s.db.Pool().Query(ctx, query, afterID, limit, from, to, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[old.Client])
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetPortalClientsFromDate returns the next page of portal (client app)
// clients, offset-paginated, restricted to the half-open window [from, to)
// on created_at: from <= created_at < to. Includes 'portal'-type chat.client
// rows belonging to the Agent app as well as the client app -- there is no
// reliable way to tell them apart by created_at alone, and filtering by
// chat.channel activity instead let clients whose first matching channel
// appeared after their created_at's migration window fall through and never
// get migrated (see .md/enhancements/migration_steps/portal_client_to_contact.md).
func (s *ClientStore) GetPortalClientsFromDate(ctx context.Context, offset, limit int, from, to time.Time) ([]*old.PortalClient, error) {
	query := `SELECT c.id,
					c.name AS name,
					null AS number,
					acc.created_at AS created_at,
					acc.updated_at AS updated_at,
					acc.profile_id AS profile_id,
					null AS first_name,
					null AS last_name,
					'salmon' AS type,
					acc.dc AS dc,
					c.name AS sub
				FROM chat.client c
				INNER JOIN portal.user_account acc ON acc.id = c.external_id::uuid
				WHERE c.type = 'portal'
				  AND c.created_at >= $3::timestamp
				  AND c.created_at < $4::timestamp
				ORDER BY c.id`

	if offset < 0 {
		offset = 0
	}

	if limit < 1 {
		limit = 1
	}

	query += ` OFFSET $1 LIMIT $2`

	rows, err := s.db.Pool().Query(ctx, query, offset, limit, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[old.PortalClient])
	if err != nil {
		return nil, err
	}

	return res, nil
}
