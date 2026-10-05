package new

import "github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid

// PeerType is stored as an integer in JSONB peer columns (from, send_to)
type PeerType int16

const (
	PeerContact PeerType = iota + 1
	PeerGroup
	PeerChannel
	PeerThread
)

// Peer is the JSONB shape used in im_message.messages.from and im_message.messages.send_to
type Peer struct {
	ID   uuid.UUID `json:"id" db:"id"`
	Type PeerType  `json:"type" db:"type"`
}
