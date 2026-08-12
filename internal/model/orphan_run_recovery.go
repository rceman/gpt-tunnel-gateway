package model

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const OrphanRunRecoverySchemaVersion = 1

const OrphanRunQuarantined = "ORPHAN_RUN_QUARANTINED"

// OrphanRunRecovery preserves an operational run that cannot be associated
// with a durable task. The original JSON is immutable evidence; it is not
// decoded into, or rewritten as, a different Run record.
type OrphanRunRecovery struct {
	SchemaVersion      int       `json:"schema_version"`
	State              string    `json:"state"`
	ProjectID          string    `json:"project_id"`
	RunID              string    `json:"run_id"`
	SourcePath         string    `json:"source_path"`
	OriginalSHA256     string    `json:"original_sha256"`
	OriginalRunJSONB64 string    `json:"original_run_json_base64"`
	Actor              string    `json:"actor"`
	Session            string    `json:"session,omitempty"`
	Reason             string    `json:"reason"`
	HubRevisionBefore  string    `json:"hub_revision_before"`
	CreatedAt          time.Time `json:"created_at"`
}

// OrphanRunRecoveryReceipt records the completed atomic reconciliation and
// is written after the immutable evidence transaction has returned its new
// Hub revision.
type OrphanRunRecoveryReceipt struct {
	SchemaVersion     int       `json:"schema_version"`
	State             string    `json:"state"`
	ProjectID         string    `json:"project_id"`
	RunID             string    `json:"run_id"`
	SourcePath        string    `json:"source_path"`
	OriginalSHA256    string    `json:"original_sha256"`
	Actor             string    `json:"actor"`
	Session           string    `json:"session,omitempty"`
	Reason            string    `json:"reason"`
	HubRevisionBefore string    `json:"hub_revision_before"`
	HubRevisionAfter  string    `json:"hub_revision_after"`
	CreatedAt         time.Time `json:"created_at"`
}

func ValidateOrphanRunRecovery(v OrphanRunRecovery) error {
	if v.SchemaVersion != OrphanRunRecoverySchemaVersion || v.State != OrphanRunQuarantined {
		return fmt.Errorf("invalid orphan run recovery identity")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateObjectIdentifier(v.RunID); err != nil {
		return fmt.Errorf("run_id: %w", err)
	}
	if strings.TrimSpace(v.SourcePath) == "" || strings.Contains(v.SourcePath, "..") || strings.HasPrefix(v.SourcePath, "/") {
		return fmt.Errorf("invalid orphan recovery source path")
	}
	if len(v.OriginalSHA256) != sha256.Size*2 {
		return fmt.Errorf("invalid orphan recovery digest")
	}
	if _, err := hex.DecodeString(v.OriginalSHA256); err != nil {
		return fmt.Errorf("invalid orphan recovery digest: %w", err)
	}
	original, err := base64.StdEncoding.DecodeString(v.OriginalRunJSONB64)
	if err != nil || len(original) == 0 {
		return fmt.Errorf("invalid orphan recovery original bytes")
	}
	digest := sha256.Sum256(original)
	if hex.EncodeToString(digest[:]) != v.OriginalSHA256 {
		return fmt.Errorf("orphan recovery digest does not match original bytes")
	}
	if strings.TrimSpace(v.Actor) == "" || strings.TrimSpace(v.Reason) == "" || strings.TrimSpace(v.HubRevisionBefore) == "" || v.CreatedAt.IsZero() {
		return fmt.Errorf("incomplete orphan recovery audit fields")
	}
	return nil
}

func ValidateOrphanRunRecoveryReceipt(v OrphanRunRecoveryReceipt) error {
	if v.SchemaVersion != OrphanRunRecoverySchemaVersion || v.State != OrphanRunQuarantined {
		return fmt.Errorf("invalid orphan recovery receipt identity")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateObjectIdentifier(v.RunID); err != nil {
		return fmt.Errorf("run_id: %w", err)
	}
	if strings.TrimSpace(v.SourcePath) == "" || strings.Contains(v.SourcePath, "..") || strings.HasPrefix(v.SourcePath, "/") {
		return fmt.Errorf("invalid orphan recovery receipt source path")
	}
	if len(v.OriginalSHA256) != sha256.Size*2 {
		return fmt.Errorf("invalid orphan recovery receipt digest")
	}
	if _, err := hex.DecodeString(v.OriginalSHA256); err != nil {
		return fmt.Errorf("invalid orphan recovery receipt digest: %w", err)
	}
	if strings.TrimSpace(v.Actor) == "" || strings.TrimSpace(v.Reason) == "" || strings.TrimSpace(v.HubRevisionBefore) == "" || strings.TrimSpace(v.HubRevisionAfter) == "" || v.CreatedAt.IsZero() {
		return fmt.Errorf("incomplete orphan recovery receipt audit fields")
	}
	return nil
}
