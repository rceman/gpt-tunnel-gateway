package model

import "time"

const ADRRevisionSchemaVersion = 1

const (
	ADRStatusAccepted   = "accepted"
	ADRStatusSuperseded = "superseded"
	ADRStatusArchived   = "archived"
)

type ADRHistoryEntry struct {
	SchemaVersion int       `json:"schema_version"`
	ADRID         string    `json:"adr_id"`
	ProjectID     string    `json:"project_id"`
	Revision      int       `json:"revision"`
	MutationKind  string    `json:"mutation_kind"`
	Actor         string    `json:"actor"`
	Reason        string    `json:"reason"`
	ChangedFields []string  `json:"changed_fields,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
}
