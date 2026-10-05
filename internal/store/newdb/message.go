package newdb

import (
	"context"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type MessageStore struct {
	store *DB
}

func NewMessageStore(store *DB) *MessageStore {
	return &MessageStore{store: store}
}

func (s *MessageStore) CreateMessages(ctx context.Context, tx pgx.Tx, messages []*modelnew.Message) error {
	if len(messages) == 0 {
		return nil
	}
	var (
		query = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Insert("im_message.messages").
			Columns(
				"id",
				"domain_id",
				"thread_id",
				"sender_id",
				"type",
				"body",
				"metadata",
				"created_at",
				"updated_at",
				"interactive",
				"member_id",
			)
	)
	for _, m := range messages {
		if m == nil {
			continue
		}
		query = query.Values(
			m.ID,
			m.DomainID,
			m.ThreadID,
			m.SenderID,
			m.Type,
			m.Body,
			m.Metadata,
			m.CreatedAt,
			m.UpdatedAt,
			m.Interactive,
			m.MemberID,
		)
	}
	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, sql, args...)

	return err
}

func (s *MessageStore) CreateDocuments(ctx context.Context, tx pgx.Tx, documents []*modelnew.MessageDocument) error {
	if len(documents) == 0 {
		return nil
	}
	var (
		query = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar).Insert("im_message.message_documents").
			Columns(
				"id",
				"message_id",
				"file_id",
				"name",
				"mime",
				"size",
				"created_at",
			).PlaceholderFormat(squirrel.Dollar)
	)
	for _, d := range documents {
		if d == nil {
			continue
		}
		query = query.Values(
			d.ID,
			d.MessageID,
			d.FileID,
			d.Name,
			d.Mime,
			d.Size,
			d.CreatedAt,
		)
	}
	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, sql, args...)

	return err
}
