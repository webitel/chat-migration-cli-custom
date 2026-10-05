package olddb

import (
	"context"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid

	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

type MessageStore struct {
	db *DB
}

func NewMessageStore(db *DB) *MessageStore {
	return &MessageStore{db: db}
}

func (s *MessageStore) GetMessagesByConversationID(ctx context.Context, conversationIDs []uuid.UUID) ([]*old.Message, error) {
	var (
		query = `SELECT
       m.id,
       m.conversation_id,
       m.channel_id,
       ch.user_id,
       ch.internal,
       m.text,
       m.created_at,
       m.updated_at,
       m.type,
       m.variables,
       m.file_id,
       m.file_size,
       m.file_type,
       m.file_name,
       m.file_url,
       m.content
FROM chat.message m
LEFT JOIN chat.channel ch ON ch.id = m.channel_id
WHERE m.conversation_id = ANY ($1)
  AND m.text IS DISTINCT FROM 'start'
ORDER BY m.created_at`
		messages []*old.Message
	)

	rows, err := s.db.Pool().Query(ctx, query, conversationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var m old.Message

		err := rows.Scan(
			&m.ID,
			&m.ConversationID,
			&m.ChannelID,
			&m.UserID,
			&m.Internal,
			&m.Text,
			&m.CreatedAt,
			&m.UpdatedAt,
			&m.Type,
			&m.Variables,
			&m.FileID,
			&m.FileSize,
			&m.FileType,
			&m.FileName,
			&m.FileURL,
			&m.Content,
		)
		if err != nil {
			return nil, err
		}

		messages = append(messages, &m)
	}

	return messages, nil
}
