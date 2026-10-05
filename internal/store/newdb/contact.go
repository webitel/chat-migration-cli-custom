package newdb

import (
	"context"

	"github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
	"github.com/jackc/pgx/v5"

	"github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type ContactStore struct {
	db *DB
}

func NewContactStore(db *DB) *ContactStore {
	return &ContactStore{db: db}
}

func (s *ContactStore) InsertContacts(ctx context.Context, tx pgx.Tx, contacts []*new.Contact) error {
	if len(contacts) == 0 {
		return nil
	}

	query := squirrel.Insert("im_contact.contact").Columns(
		"id",
		"domain_id",
		"created_at",
		"updated_at",
		"issuer_id",
		"application_id",
		"subject_id",
		"type",
		"name",
		"username",
		"metadata",
		"is_bot",
	).PlaceholderFormat(squirrel.Dollar)

	for _, contact := range contacts {
		query = query.Values(
			contact.ID,
			contact.DomainID,
			contact.CreatedAt,
			contact.UpdatedAt,
			contact.IssuerID,
			contact.ApplicationID,
			contact.SubjectID,
			contact.Type,
			contact.Name,
			contact.Username,
			contact.Metadata,
			contact.IsBot,
		)
	}

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}

	return nil
}

// insertContactsIgnoreConflictsResult is one row of
// InsertContactsIgnoreConflicts' RETURNING clause -- enough to resolve which
// input contact (matched by the contact_issuer_subject_unique key) got which
// real im_contact.contact.id, and whether that row was freshly inserted.
type insertContactsIgnoreConflictsResult struct {
	ID        uuid.UUID `db:"id"`
	DomainID  int       `db:"domain_id"`
	IssuerID  string    `db:"issuer_id"`
	SubjectID string    `db:"subject_id"`
	Inserted  bool      `db:"inserted"`
}

type contactKey struct {
	DomainID  int
	IssuerID  string
	SubjectID string
}

// InsertContactsIgnoreConflicts upserts contacts on the
// contact_issuer_subject_unique key (domain_id, issuer_id, subject_id):
// a genuinely new contact is inserted as given, a conflicting one is left
// untouched (DO UPDATE SET id = id is a no-op self-assignment, it exists
// only to make the conflicting row go through the DO UPDATE branch so
// RETURNING still yields it -- DO NOTHING would silently drop it). Every
// contact in contacts is mutated in place: its ID becomes the row's real id
// in im_contact.contact -- unchanged for a fresh insert, replaced with the
// pre-existing row's id on conflict -- so callers that build chat_migration
// rows or other references from contact.ID after this call never point at
// a row that was never inserted. The returned count is the number of
// contacts actually inserted (xmax = 0), same meaning as before this
// function upserted instead of skipping conflicts.
func (s *ContactStore) InsertContactsIgnoreConflicts(ctx context.Context, tx pgx.Tx, contacts []*new.Contact) (int64, error) {
	if len(contacts) == 0 {
		return 0, nil
	}

	query := squirrel.Insert("im_contact.contact").Columns(
		"id",
		"domain_id",
		"created_at",
		"updated_at",
		"issuer_id",
		"application_id",
		"subject_id",
		"type",
		"name",
		"username",
		"metadata",
		"is_bot",
	).PlaceholderFormat(squirrel.Dollar).Suffix(
		"ON CONFLICT (domain_id, issuer_id, subject_id) DO UPDATE SET id = im_contact.contact.id " +
			"RETURNING id, domain_id, issuer_id, subject_id, (xmax = 0) AS inserted",
	)

	for _, contact := range contacts {
		query = query.Values(
			contact.ID,
			contact.DomainID,
			contact.CreatedAt,
			contact.UpdatedAt,
			contact.IssuerID,
			contact.ApplicationID,
			contact.SubjectID,
			contact.Type,
			contact.Name,
			contact.Username,
			contact.Metadata,
			contact.IsBot,
		)
	}

	sql, args, err := query.ToSql()
	if err != nil {
		return 0, err
	}

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return 0, err
	}

	results, err := pgx.CollectRows(rows, pgx.RowToStructByName[insertContactsIgnoreConflictsResult])
	if err != nil {
		return 0, err
	}

	resolved := make(map[contactKey]uuid.UUID, len(results))

	var inserted int64

	for _, r := range results {
		resolved[contactKey{DomainID: r.DomainID, IssuerID: r.IssuerID, SubjectID: r.SubjectID}] = r.ID
		if r.Inserted {
			inserted++
		}
	}

	for _, contact := range contacts {
		contact.ID = resolved[contactKey{DomainID: contact.DomainID, IssuerID: contact.IssuerID, SubjectID: contact.SubjectID}]
	}

	return inserted, nil
}

func (s *ContactStore) SyncContactVias(ctx context.Context, tx pgx.Tx) (int64, error) {
	query := `WITH chain AS (SELECT ct.new_id contact_id, gt.new_id gate_id
               FROM public.chat_migration ct
                        LEFT JOIN public.chat_migration gt
                                  ON gt.old_id = ct.old_id AND gt.entity_type = 'provider_to_gateway' AND
                                     ct.domain_id = gt.domain_id
               WHERE ct.entity_type = 'gateway_to_contact' AND gt.new_id IS NOT NULL)

INSERT
INTO im_contact.via(contact_id, via)
SELECT contact_id, gate_id
FROM chain
ON CONFLICT (contact_id, via) DO NOTHING;
`

	tag, err := tx.Exec(ctx, query)
	if err != nil {
		return 0, err
	}

	return tag.RowsAffected(), nil
}

func (s *ContactStore) GetByWebitelUserIDs(ctx context.Context, tx pgx.Tx, webitelUserIDs []string) ([]*new.Contact, error) {
	if len(webitelUserIDs) == 0 {
		return nil, nil
	}

	query := `
		SELECT id, domain_id, created_at, updated_at, issuer_id, application_id, subject_id, type, name, username, metadata, is_bot
		FROM im_contact.contact
		WHERE subject_id = ANY($1::text[]) AND issuer_id = 'webitel' AND is_bot = false`

	rows, err := tx.Query(ctx, query, webitelUserIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[new.Contact])
	if err != nil {
		return nil, err
	}

	return result, nil
}
