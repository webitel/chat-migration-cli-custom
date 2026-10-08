package olddb

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webitel/chat-migration-cli/internal/model/old"
)

type ConversationStore struct {
	db *DB
}

func NewConversationStore(db *DB) *ConversationStore {
	return &ConversationStore{db: db}
}

// GetGroupedConversationsByInitiatorFromDate groups closed conversations by
// initiator (one group per client, regardless of flow), keyset-paginated after
// lastSeenInitiatorID, restricted to the half-open window [from, to) on
// closed_at: from <= closed_at < to. flowIDs restricts the result to
// conversations whose props->>'flow' is in that list
// (public.bot_mapping.old_bot_id); clientTypes restricts it to initiators whose
// chat.client.type (NULL treated as 'webchat', same as clients_to_contacts) is
// in that list (public.bot_mapping.type).
func (s *ConversationStore) GetGroupedConversationsByInitiatorFromDate(ctx context.Context, lastSeenInitiatorID int, limit int, from, to time.Time, flowIDs []int32, clientTypes []string) ([]*old.GroupedConversation, error) {
	var (
		query = `
		WITH conversations AS (SELECT conv.id id,
                              initiator.user_id       initiator,
                              COALESCE(cl.type, 'webchat') client_type,
                              conv.title,
                              conv.domain_id,
                              conv.created_at
                       FROM chat.conversation conv
                                INNER JOIN chat.channel initiator
                                          ON initiator.conversation_id = conv.id AND NOT initiator.internal
                                INNER JOIN chat.client cl ON cl.id = initiator.user_id
                       WHERE conv.closed_at IS NOT NULL
                        AND conv.props ->> 'flow' IS NOT NULL
                        AND (conv.props ->> 'flow')::int = ANY($5::int[])
                        AND COALESCE(cl.type, 'webchat') = ANY($6::text[])
                        AND initiator.user_id > $1
                        AND conv.closed_at >= $2
                        AND conv.closed_at < $3),
     grouped_conversations AS (SELECT ARRAY_AGG(conv.id)                   conv_ids,
                                      initiator,
                                      client_type,
                                      (MAX(DISTINCT conv.title)) "title",
                                      (ARRAY_AGG(conv.domain_id))[1]       domain_id,
                                      (ARRAY_AGG(conv.created_at))[1]      created_at
                               FROM conversations conv
                               GROUP BY (conv.initiator, conv.client_type)
                               ORDER BY conv.initiator
                               LIMIT $4
     )


SELECT *
FROM grouped_conversations conv
LEFT JOIN LATERAL (SELECT JSONB_AGG(users.user) internal_users
                            FROM (SELECT JSONB_BUILD_OBJECT('channel_ids', array_agg(ch.id),
                                                            'user_id', ch.user_id,
                                                            'created_at', min(ch.created_at),
                                                            'closed_at', max(ch.closed_at),
                                                            'leave_reason', max(ch.closed_cause),
                                                            'name', max(us.name)
                                         ) "user"
                                  FROM chat.channel ch
                                  LEFT JOIN directory.wbt_user us ON ch.user_id = us.id
                                  WHERE ch.conversation_id = ANY (conv.conv_ids)
                                    AND ch.internal
                                  GROUP BY user_id) users) users ON true
`
	)
	rows, err := s.db.Pool().Query(ctx, query, lastSeenInitiatorID, from, to, limit, flowIDs, clientTypes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[old.GroupedConversation])
	if err != nil {
		return nil, err
	}

	return result, nil
}
