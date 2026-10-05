package newdb

import (
	"context"
	"strconv"

	"github.com/Masterminds/squirrel"
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
