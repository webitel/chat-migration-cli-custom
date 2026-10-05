package newdb

import (
	"context"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type DirectSettingsStore struct {
	store *DB
}

func NewDirectSettingsStore(store *DB) *DirectSettingsStore {
	return &DirectSettingsStore{store: store}
}

// InsertDirectSettings inserts one im_thread.direct_settings row per
// thread_dialog -- settings is the same per-page slice InsertThreadDialogs
// receives, with one entry per created thread_dialog (owner and member
// alike).
func (s *DirectSettingsStore) InsertDirectSettings(ctx context.Context, tx pgx.Tx, settings []*modelnew.DirectSettings) error {
	if len(settings) == 0 {
		return nil
	}

	query := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).
		Insert("im_thread.direct_settings").
		Columns("thread_dialog_id", "domain_id", "title")
	for _, setting := range settings {
		query = query.Values(setting.ThreadDialogID, setting.DomainID, setting.Title)
	}

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, sql, args...)

	return err
}
