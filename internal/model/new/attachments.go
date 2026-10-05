package new

import (
	"time"

	"github.com/gofrs/uuid/v5" //nolint:depguard // NewV7AtTime is not available in google/uuid
)

type MessageAttachment interface {
	GetFileID() int64
}

type MessageImage struct {
	ID         uuid.UUID      `json:"id" db:"id"`
	MessageID  uuid.UUID      `json:"message_id" db:"message_id"`
	FileID     int64          `json:"file_id" db:"file_id"`
	Name       string         `json:"name" db:"name"`
	Mime       string         `json:"mime" db:"mime"`
	Width      int32          `json:"width,omitempty" db:"width"`
	Height     int32          `json:"height,omitempty" db:"height"`
	CreatedAt  time.Time      `json:"created_at" db:"created_at"`
	Thumbnails map[string]any `json:"thumbnails,omitempty" db:"thumbnails"`
	URL        string         `json:"url"`
}

func (mi *MessageImage) GetFileID() int64 { return mi.FileID }

type MessageDocument struct {
	ID        uuid.UUID `json:"id" db:"id"`
	MessageID uuid.UUID `json:"message_id" db:"message_id"`
	FileID    int64     `json:"file_id" db:"file_id"`
	Name      string    `json:"name" db:"name"`
	Mime      string    `json:"mime" db:"mime"`
	Size      int64     `json:"size" db:"size"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	URL       string    `json:"url,omitempty"`
}

func (md *MessageDocument) GetFileID() int64 { return md.FileID }
