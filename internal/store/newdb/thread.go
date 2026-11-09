package newdb

import (
	"context"
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

func (s *ThreadStore) InitBotControl(ctx context.Context, tx pgx.Tx, dialogs []*newmodel.ThreadDialog) error {
	var (
		threadIDs = make([]uuid.UUID, 0, len(dialogs))
		botIDs    = make([]uuid.UUID, 0, len(dialogs))
	)

	for _, dialog := range dialogs {
		if !dialog.IsBot {
			continue
		}

		threadIDs = append(threadIDs, dialog.ThreadID)
		botIDs = append(botIDs, dialog.ID)
	}

	if len(botIDs) == 0 {
		return nil
	}

	_, err := tx.Exec(ctx, `
		WITH pushed AS (
			INSERT INTO im_thread.bot_control_stack (thread_id, member_id, position)
			SELECT thread_id, member_id, 0
			FROM unnest($1::uuid[], $2::uuid[]) AS o(thread_id, member_id)
			RETURNING thread_id, member_id
		)
		UPDATE im_thread.thread t
		SET owner_bot_id = p.member_id,
		    bot_controller_id = p.member_id
		FROM pushed p
		WHERE t.id = p.thread_id`, threadIDs, botIDs)
	if err != nil {
		return annotatePgError(err)
	}

	return nil
}
