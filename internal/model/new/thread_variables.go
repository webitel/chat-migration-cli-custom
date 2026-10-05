package new

import (
	"time"

	"github.com/gofrs/uuid/v5"
)

type VariableEntry struct {
	Value map[string]any `json:"value"`
	SetBy uuid.UUID      `json:"set_by"`
	SetAt time.Time      `json:"set_at"`
}

type ThreadVariables struct {
	ThreadID  uuid.UUID                `json:"thread_id" db:"thread_id"`
	Variables map[string]VariableEntry `json:"variables" db:"variables"`
}
