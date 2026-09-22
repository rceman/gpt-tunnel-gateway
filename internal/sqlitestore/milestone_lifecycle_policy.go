package sqlitestore

import (
	"encoding/json"
	"fmt"
)

func validateMilestoneMutation(definition sharedLifecycleDefinition, previousPayload, payload []byte, kind, historyKind string) error {
	if definition.EntityType == "track" {
		return validateTrackMutation(previousPayload, payload, kind)
	}
	if definition.EntityType != "milestone" {
		return nil
	}
	var previous, next struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(previousPayload, &previous); err != nil {
		return fmt.Errorf("invalid shared milestone previous payload")
	}
	if err := json.Unmarshal(payload, &next); err != nil {
		return fmt.Errorf("invalid shared milestone payload")
	}
	if previous.Status == next.Status {
		if previous.Status == "completed" || previous.Status == "archived" {
			return fmt.Errorf("shared milestone status %q is terminal for content mutations", previous.Status)
		}
		return nil
	}
	wantKind, wantHistory := "", ""
	switch {
	case previous.Status == "planned" && next.Status == "active":
		wantKind, wantHistory = "milestone-activate", "activate"
	case previous.Status == "active" && next.Status == "completed":
		wantKind, wantHistory = "milestone-complete", "complete"
	case previous.Status == "completed" && next.Status == "archived":
		wantKind, wantHistory = "archive", "archive"
	default:
		return fmt.Errorf("invalid shared milestone lifecycle transition")
	}
	if kind != wantKind || historyKind != wantHistory {
		return fmt.Errorf("shared milestone transition requires %s", wantKind)
	}
	return nil
}
