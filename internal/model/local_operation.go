package model

import (
	"fmt"
	"strings"
	"time"
)

const LocalOperationSchemaVersion = 1

// LocalOperation is the compact, Gateway-owned operation projection. It is
// deliberately local: Hub transactions and their remote receipts are not
// required to read operation status.
type LocalOperation struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	EntityID      string    `json:"entity_id,omitempty"`
	RequestSHA256 string    `json:"request_sha256,omitempty"`
	Result        string    `json:"result,omitempty"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func ValidateLocalOperation(v LocalOperation) error {
	if v.SchemaVersion != LocalOperationSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil {
		return fmt.Errorf("invalid local operation identity")
	}
	code, _, err := ParseOperatorEventID(v.ID)
	if err != nil || code != v.ProjectCode() {
		return fmt.Errorf("invalid local operation id")
	}
	if strings.TrimSpace(v.Kind) == "" || len(v.Kind) > 128 || strings.ContainsAny(v.Kind, "\x00\r\n") {
		return fmt.Errorf("invalid local operation kind")
	}
	switch v.Status {
	case "accepted", "running", "completed", "failed", "outcome_unknown":
	default:
		return fmt.Errorf("invalid local operation status %q", v.Status)
	}
	if v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
		return fmt.Errorf("invalid local operation timestamps")
	}
	return nil
}

// ProjectCode derives the compact ID prefix without making callers duplicate
// identifier parsing logic. The service validates the prefix against local
// project configuration before persisting the record.
func (v LocalOperation) ProjectCode() string {
	code, _, _ := ParseOperatorEventID(v.ID)
	return code
}
