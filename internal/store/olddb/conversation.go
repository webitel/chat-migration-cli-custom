package olddb

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

type ConversationStore struct {
	db *DB
}

func NewConversationStore(db *DB) *ConversationStore {
	return &ConversationStore{db: db}
}

// GetGroupedConversationsByUsersAndFlowFromDate groups closed conversations
// by (initiator, flow_id), keyset-paginated after (lastSeenInitiatorID,
// lastSeenFlowID), restricted to the half-open window [from, to) on
// closed_at: from <= closed_at < to. flowIDs restricts the result to
// conversations whose props->>'flow' is in that list
// (public.bot_mapping.old_bot_id).
func (s *ConversationStore) GetGroupedConversationsByUsersAndFlowFromDate(ctx context.Context, lastSeenInitiatorID int, lastSeenFlowID int, limit int, from, to time.Time, flowIDs []int32) ([]*old.GroupedConversation, error) {
	var (
		query = `
		WITH conversations AS (SELECT conv.id id,
                              initiator.user_id       initiator,
                              (conv.props ->> 'flow') flow_id,
                              conv.title,
                              conv.domain_id,
                              conv.created_at
                       FROM chat.conversation conv
                                INNER JOIN chat.channel initiator
                                          ON initiator.conversation_id = conv.id AND NOT initiator.internal
                       WHERE conv.closed_at IS NOT NULL
                        AND conv.props ->> 'flow' IS NOT NULL
                        AND (conv.props ->> 'flow')::int = ANY($6::int[])
                        AND (initiator.user_id, conv.props ->> 'flow') > ($1,$2::text)
                        AND conv.closed_at >= $3
                        AND conv.closed_at < $4),
     grouped_conversations AS (SELECT ARRAY_AGG(conv.id)                   conv_ids,
                                      initiator,
                                      flow_id::bigint,
                                      (MAX(DISTINCT conv.title)) "title",
                                      (ARRAY_AGG(conv.domain_id))[1]       domain_id,
                                      (ARRAY_AGG(conv.created_at))[1]      created_at
                               FROM conversations conv
                               GROUP BY (conv.initiator, flow_id)
                               ORDER BY conv.initiator, flow_id
                               LIMIT $5
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
	rows, err := s.db.Pool().Query(ctx, query, lastSeenInitiatorID, strconv.Itoa(lastSeenFlowID), from, to, limit, flowIDs)
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
