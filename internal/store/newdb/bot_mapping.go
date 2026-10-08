package newdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	modelnew "github.com/webitel/chat-migration-cli-custom/internal/model/new"
)

type BotMappingStore struct {
	db *DB
}

func NewBotMappingStore(db *DB) *BotMappingStore {
	return &BotMappingStore{db: db}
}

// GetTypes returns the distinct values of public.bot_mapping.type, used to filter
// which old-DB client types are eligible for migration in clients_to_contacts.
func (s *BotMappingStore) GetTypes(ctx context.Context) ([]string, error) {
	rows, err := s.db.pool.Query(ctx, `SELECT DISTINCT type FROM public.bot_mapping`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var types []string

	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}

		types = append(types, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return types, nil
}

// GetByType returns, for every distinct public.bot_mapping.type, the row that
// represents that client type: the one with the smallest new_bot_id (ties
// broken by old_bot_id). Used by members and messages to pick the owner bot
// (and its gate) of a thread by the type of the thread's client.
func (s *BotMappingStore) GetByType(ctx context.Context) (map[string]*modelnew.BotTypeMapping, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT DISTINCT ON (type) type, old_bot_id, new_bot_id, gate_id
		FROM public.bot_mapping
		ORDER BY type, new_bot_id, old_bot_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[modelnew.BotTypeMapping])
	if err != nil {
		return nil, err
	}

	result := make(map[string]*modelnew.BotTypeMapping, len(list))
	for _, m := range list {
		result[m.Type] = m
	}

	return result, nil
}

// GetAllOldBotIDs returns every public.bot_mapping.old_bot_id value, regardless
// of type -- used to restrict which old-DB flow_ids (conversations, and
// downstream steps that re-derive the same conversation grouping) are
// eligible for migration.
func (s *BotMappingStore) GetAllOldBotIDs(ctx context.Context) ([]int32, error) {
	rows, err := s.db.pool.Query(ctx, `SELECT old_bot_id FROM public.bot_mapping`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int32

	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

// GetGateMappings returns every public.bot_mapping row of type = 'facebook'
// that has a manually configured gate_id, used by facebook_and_whatsapp to
// link old providers (resolved to chat.bot.id via old_bot_id/flow_id) to
// their pre-existing im_provider.gates row. Rows with gate_id IS NULL (bots
// without a Facebook/WhatsApp gate) are excluded by the query itself.
func (s *BotMappingStore) GetGateMappings(ctx context.Context) ([]*modelnew.BotGateMapping, error) {
	rows, err := s.db.pool.Query(ctx, `SELECT old_bot_id, gate_id FROM public.bot_mapping WHERE type = 'facebook' AND gate_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[modelnew.BotGateMapping])
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetAll assumes schema/table are already split and validated by the caller. The
// identifier is quoted via pgx.Identifier.Sanitize() -- never naive concatenation --
// since schema/table ultimately come from client-supplied config.
func (s *BotMappingStore) GetAll(ctx context.Context, schema, table string) ([]*modelnew.BotMapping, error) {
	ident := pgx.Identifier{schema, table}.Sanitize()
	query := fmt.Sprintf("SELECT old_bot_id, new_bot_id FROM %s", ident)

	rows, err := s.db.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to read bot mapping table %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[modelnew.BotMapping])
	if err != nil {
		return nil, fmt.Errorf("failed to scan bot mapping table %s.%s (check for NULL old_bot_id/new_bot_id): %w", schema, table, err)
	}

	return result, nil
}
