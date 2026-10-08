package new

import "github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid

// BotMapping mirrors a row of the client-managed public.bot_mapping table.
// The tool never creates, seeds, or validates this table's contents against
// existing contacts.
type BotMapping struct {
	OldBotID int       `db:"old_bot_id"`
	NewBotID uuid.UUID `db:"new_bot_id"`
}

// BotGateMapping is a public.bot_mapping row that also carries a manually
// configured gate_id -- the pre-existing im_provider.gates row for that
// flow's Facebook/WhatsApp provider. Only rows with gate_id set are ever
// returned as this type; not every bot has a gate.
type BotGateMapping struct {
	OldBotID int       `db:"old_bot_id"`
	GateID   uuid.UUID `db:"gate_id"`
}

// BotTypeMapping is the public.bot_mapping row chosen to represent a client
// type: the row of that type with the smallest new_bot_id (ties broken by
// old_bot_id). GateID is nil when the row has no manually configured gate.
type BotTypeMapping struct {
	Type     string     `db:"type"`
	OldBotID int        `db:"old_bot_id"`
	NewBotID uuid.UUID  `db:"new_bot_id"`
	GateID   *uuid.UUID `db:"gate_id"`
}
