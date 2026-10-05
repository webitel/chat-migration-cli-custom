package new

import "github.com/gofrs/uuid/v5"

type EntityType string

const (
	EntityTypeClientContact                EntityType = "client_contact"
	EntityTypeBotContact                   EntityType = "bot_contact"
	EntityTypeConversationThread           EntityType = "conversation_thread"
	EntityTypeFlowIDAndInitiatorIDToThread EntityType = "flow_id_and_initiator_id_to_thread"
	EntityTypeInitiatorChannelThreadDialog EntityType = "initiator_channel_thread_dialog"
	EntityTypeBotChannelThreadDialog       EntityType = "bot_channel_thread_dialog"
	EntityTypeInternalChannelThreadDialog  EntityType = "internal_channel_thread_dialog"
	EntityTypeMessage                      EntityType = "message"
	EntityTypeGatewayToContact             EntityType = "gateway_to_contact"
	EntityTypeProviderToGateway            EntityType = "provider_to_gateway"
	EntityTypePortalAppAccount             EntityType = "portal_app_account"
)

type MigrationRowFilters struct {
	Offset    int
	Limit     int
	Type      []EntityType
	OldIDs    []string
	ExtraKeys []string
	DomainID  int
}

type MigrationRow struct {
	ID         uuid.UUID  `db:"id"`
	EntityType EntityType `db:"entity_type"`
	OldID      string     `db:"old_id"`
	NewID      uuid.UUID  `db:"new_id"`
	DomainID   int        `db:"domain_id"`
	ExtraKey   *string    `db:"extra_key"`
	SessionID  uuid.UUID  `db:"session_id"`
}
