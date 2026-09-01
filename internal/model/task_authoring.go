package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func HashTaskAuthoring(v TaskAuthoring) (string, error) {
	// Omit the default so records written before Task.Type existed retain
	// their semantic revision identity.
	if v.Type == TaskTypeTask {
		v.Type = ""
	}
	v.RevisionSHA256 = ""
	v.Status = TaskAuthoringPlanned
	v.ReadySeal = nil
	v.CreatedAt = v.CreatedAt.UTC()
	// UpdatedAt records the write event, not the semantic revision. Exclude it
	// so a ready seal can update operational metadata without changing the
	// revision identity.
	v.UpdatedAt = time.Time{}
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateTaskAuthoring(v TaskAuthoring) error {
	if v.SchemaVersion != TaskAuthoringSchemaVersion || ValidateCanonicalTaskID(v.ID) != nil || ValidateProjectIdentifier(v.ProjectID) != nil {
		return fmt.Errorf("invalid task authoring identity")
	}
	if len(v.Title) < 3 || len(v.Title) > 300 || len(v.Objective) < 3 || len(v.Objective) > 200000 {
		return fmt.Errorf("invalid task authoring content")
	}
	if _, err := NormalizeTaskType(v.Type); err != nil {
		return err
	}
	if len(v.AcceptanceCriteria) > 200 || len(v.Constraints) > 200 || len(v.Dependencies) > 64 || len(v.PreparationReferences) > 64 || len(v.Metadata) > 64 {
		return fmt.Errorf("task authoring bounds exceeded")
	}
	for _, value := range append(append(append([]string{}, v.AcceptanceCriteria...), v.Constraints...), append(v.Dependencies, v.PreparationReferences...)...) {
		if strings.TrimSpace(value) == "" || len([]byte(value)) > 20000 || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("invalid task authoring entry")
		}
	}
	if v.Priority != "" && (len(v.Priority) > 32 || strings.ContainsAny(v.Priority, "\x00\r\n")) {
		return fmt.Errorf("invalid task authoring priority")
	}
	for key, value := range v.Metadata {
		if strings.TrimSpace(key) == "" || len(key) > 64 || len(value) > 1024 || strings.ContainsAny(key+value, "\x00\r\n") {
			return fmt.Errorf("invalid task authoring metadata")
		}
	}
	switch v.ADRRelation {
	case TaskADRNoRequired:
		if len(v.ADRReferences) != 0 {
			return fmt.Errorf("no_adr_required cannot include ADR references")
		}
	case TaskADRImplementsExisting, TaskADRRequiresNew, TaskADRSupersedesExisting:
		if len(v.ADRReferences) == 0 || len(v.ADRReferences) > 8 {
			return fmt.Errorf("ADR relation requires bounded ADR references")
		}
		seen := map[string]bool{}
		for _, id := range v.ADRReferences {
			if ValidateADRIdentifier(id) != nil && ValidateCanonicalADRIdentifier(id) != nil {
				return fmt.Errorf("invalid ADR reference %q", id)
			}
			if seen[id] {
				return fmt.Errorf("duplicate ADR reference %q", id)
			}
			seen[id] = true
		}
	default:
		return fmt.Errorf("invalid ADR relation")
	}
	if v.Revision < 1 || len(v.RevisionSHA256) != 64 || strings.Trim(v.RevisionSHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid task authoring revision")
	}
	if v.CreatedBy == "" || strings.ContainsAny(v.CreatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid task authoring metadata")
	}
	if v.Status != TaskAuthoringPlanned && v.Status != TaskAuthoringReady {
		return fmt.Errorf("invalid task authoring status")
	}
	want, err := HashTaskAuthoring(v)
	if err != nil || want != v.RevisionSHA256 {
		return fmt.Errorf("task authoring revision hash mismatch")
	}
	if v.Status == TaskAuthoringPlanned {
		if v.ReadySeal != nil {
			return fmt.Errorf("planned task cannot have ready seal")
		}
	} else {
		if v.ReadySeal == nil || v.ReadySeal.Revision != v.Revision || v.ReadySeal.RevisionSHA256 != v.RevisionSHA256 || v.ReadySeal.ReadyBy == "" || v.ReadySeal.ReadyAt.IsZero() {
			return fmt.Errorf("ready task has invalid ready seal")
		}
	}
	return nil
}
