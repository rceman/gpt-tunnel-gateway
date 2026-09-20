package sqlitestore

import (
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// SharedLifecycleStatusValues exposes the descriptor-owned status vocabulary
// for schemas and other boundary projections. The returned slice is detached
// from the registry so callers cannot mutate policy.
func SharedLifecycleStatusValues(entityType string, forCreate bool) []string {
	definition, ok := sharedLifecycle(entityType)
	if !ok {
		return nil
	}
	values := definition.AllowedStatuses
	if forCreate {
		values = definition.AllowedCreateStatuses
	}
	return append([]string(nil), values...)
}

func validateSharedLifecycleStatus(definition sharedLifecycleDefinition, previousPayload, payload []byte, creating bool) error {
	if definition.DefaultCreateStatus == "" {
		return nil
	}
	var next, previous struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(payload, &next); err != nil || next.Status == "" || !containsString(definition.AllowedStatuses, next.Status) {
		return fmt.Errorf("invalid shared %s status", definition.EntityType)
	}
	if definition.EntityType == "milestone" {
		var milestone model.Milestone
		if err := json.Unmarshal(payload, &milestone); err != nil || model.ValidateMilestone(milestone) != nil {
			return fmt.Errorf("invalid shared milestone payload")
		}
	}
	if creating {
		if !containsString(definition.AllowedCreateStatuses, next.Status) {
			return fmt.Errorf("invalid shared %s create status", definition.EntityType)
		}
		return nil
	}
	if err := json.Unmarshal(previousPayload, &previous); err != nil || previous.Status == "" || !containsString(definition.AllowedStatuses, previous.Status) {
		return fmt.Errorf("invalid shared %s previous status", definition.EntityType)
	}
	if previous.Status == next.Status {
		return nil
	}
	if !containsString(definition.AllowedTransitions[previous.Status], next.Status) {
		return fmt.Errorf("invalid shared %s status transition %q to %q", definition.EntityType, previous.Status, next.Status)
	}
	return nil
}

func validateSharedLifecycleEventKind(definition sharedLifecycleDefinition, eventKind, toStatus string) error {
	if definition.ArchiveStatus == "" {
		return nil
	}
	switch eventKind {
	case SharedLifecycleEventKindArchive:
		if toStatus == definition.ArchiveStatus {
			return nil
		}
	case SharedLifecycleEventKindStatus:
		if toStatus != definition.ArchiveStatus {
			return nil
		}
	}
	return fmt.Errorf("invalid shared %s %s lifecycle event target status", definition.EntityType, eventKind)
}

func sharedLifecycleMutationKind(definition sharedLifecycleDefinition, eventKind string) string {
	switch eventKind {
	case SharedLifecycleEventKindStatus:
		return definition.StatusMutationKind
	case SharedLifecycleEventKindArchive:
		return definition.ArchiveMutationKind
	}
	return ""
}

// ApplySharedLifecycleCreateDefaults applies the descriptor-owned create
// status before an entity-specific validator runs.
func ApplySharedLifecycleCreateDefaults(entityType string, payload []byte) ([]byte, error) {
	definition, ok := sharedLifecycle(entityType)
	if !ok {
		return nil, fmt.Errorf("unsupported shared lifecycle %q", entityType)
	}
	if definition.DefaultCreateStatus == "" {
		return append([]byte(nil), payload...), nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	status, ok := fields["status"]
	if !ok || string(status) == `""` || string(status) == "null" {
		fields["status"], _ = json.Marshal(definition.DefaultCreateStatus)
	}
	return json.Marshal(fields)
}
