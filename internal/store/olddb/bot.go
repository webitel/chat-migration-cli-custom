package olddb

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/webitel/chat-migration-cli-custom/internal/model/old"
)

type BotStore struct {
	db *DB
}

func NewBotStore(db *DB) *BotStore {
	return &BotStore{db: db}
}

// GetProviderIDsByFlowIDs resolves chat.bot.id for the given flow_ids
// (chat.bot.flow_id) -- used by facebook_and_whatsapp to translate the
// flow_id key of public.bot_mapping into the provider id that
// clients_to_contacts already recorded as chat_migration's
// gateway_to_contact.old_id (chat.channel.connection). Only provider = 'messenger'
// rows are considered, matching the old gateway-creation logic this replaces.
func (s *BotStore) GetProviderIDsByFlowIDs(ctx context.Context, flowIDs []int) ([]*old.ProviderRef, error) {
	if len(flowIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Pool().Query(ctx, `
		SELECT id, flow_id FROM chat.bot
		WHERE provider = 'messenger' AND flow_id = ANY($1)`, flowIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[old.ProviderRef])
}
