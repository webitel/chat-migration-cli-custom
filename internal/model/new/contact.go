package new

type Contact struct {
	BaseModel

	IssuerID      string `json:"issuer_id" db:"issuer_id"`
	SubjectID     string `json:"subject_id" db:"subject_id"`
	ApplicationID string `json:"application_id" db:"application_id"`
	Type          string `json:"type" db:"type"`

	Name     string            `json:"name" db:"name"`
	Username string            `json:"username" db:"username"`
	Metadata map[string]string `json:"metadata" db:"metadata"`
	IsBot    bool              `json:"is_bot" db:"is_bot"`
}
