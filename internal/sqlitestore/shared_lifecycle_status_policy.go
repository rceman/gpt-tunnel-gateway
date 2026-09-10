package sqlitestore

import (
	"encoding/json"
	"fmt"
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
