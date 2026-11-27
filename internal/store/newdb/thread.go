package newdb

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
	"github.com/jackc/pgx/v5"

	newmodel "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type ThreadStore struct {
	db *DB
}

func NewThreadStore(db *DB) *ThreadStore {
	return &ThreadStore{db: db}
}

func (s *ThreadStore) InsertThreads(ctx context.Context, tx pgx.Tx, threads []*newmodel.Thread) error {
	if len(threads) == 0 {
		return nil
	}

	query := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Insert("im_thread.thread").Columns(
		"id",
		"domain_id",
		"created_at",
		"updated_at",
		"kind",
		"owner",
		"subject",
		"description",
	)
	for _, thread := range threads {
		query = query.Values(
			thread.ID,
			thread.DomainID,
			thread.CreatedAt,
			thread.UpdatedAt,
			strconv.Itoa(int(thread.Kind)),
			thread.Owner,
			thread.Subject,
			thread.Description,
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

func (s *ThreadStore) FindLiveDirectThreads(ctx context.Context, tx pgx.Tx, pairs []newmodel.DirectPair) (map[newmodel.DirectPair]uuid.UUID, error) {
	if len(pairs) == 0 {
		return make(map[newmodel.DirectPair]uuid.UUID), nil
	}

	var (
		domainIDs = make([]int, 0, len(pairs))
		clientIDs = make([]uuid.UUID, 0, len(pairs))
		botIDs    = make([]uuid.UUID, 0, len(pairs))
	)

	for _, p := range pairs {
		domainIDs = append(domainIDs, p.DomainID)
		clientIDs = append(clientIDs, p.ClientID)
		botIDs = append(botIDs, p.BotID)
	}

	rows, err := tx.Query(ctx, `
		SELECT p.domain_id, p.client_id, p.bot_id, t.id
		FROM unnest($1::int[], $2::uuid[], $3::uuid[]) AS p(domain_id, client_id, bot_id)
		CROSS JOIN LATERAL (
			SELECT t.id
			FROM im_thread.thread t
			JOIN im_thread.thread_dialog c
			  ON c.thread_id = t.id AND c.member_id = p.client_id AND c.deleted_at IS NULL
			JOIN im_thread.thread_dialog b
			  ON b.thread_id = t.id AND b.member_id = p.bot_id AND b.deleted_at IS NULL
			WHERE t.domain_id = p.domain_id
			  AND t.kind = $4
			ORDER BY t.created_at, t.id
			LIMIT 1
		) t`, domainIDs, clientIDs, botIDs, int(newmodel.ThreadDirect))
	if err != nil {
		return nil, fmt.Errorf("find live direct threads: %w", err)
	}
	defer rows.Close()

	found := make(map[newmodel.DirectPair]uuid.UUID, len(pairs))

	for rows.Next() {
		var (
			p        newmodel.DirectPair
			threadID uuid.UUID
		)

		if err := rows.Scan(&p.DomainID, &p.ClientID, &p.BotID, &threadID); err != nil {
			return nil, fmt.Errorf("scan live direct thread: %w", err)
		}

		found[p] = threadID
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live direct threads: %w", err)
	}

	return found, nil
}
