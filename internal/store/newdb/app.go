package newdb

import (
	"context"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type AppStore struct {
	db *DB
}

func NewAppStore(db *DB) *AppStore {
	return &AppStore{db: db}
}

func (s *AppStore) InsertApps(ctx context.Context, tx pgx.Tx, apps []*new.App) error {
	if len(apps) == 0 {
		return nil
	}
	var (
		query = squirrel.Insert("im_account.app").Columns(
			"dc",
			"id",
			"name",
			"about",
			"config",
			"created_at",
			"updated_at",
			"revoked_at",
		).PlaceholderFormat(squirrel.Dollar)
	)

	for _, app := range apps {
		query = query.Values(
			app.DomainID,
			app.ID,
			app.Name,
			app.About,
			app.Config,
			app.CreatedAt,
			app.UpdatedAt,
			app.RevokedAt,
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
