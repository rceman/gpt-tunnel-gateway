package service

import (
	"encoding/json"
	"fmt"
	"time"
)

func migrationAttemptStatus(status string, finishedAt *time.Time) (string, error) {
	switch status {
	case "created", "dispatching", "dispatched", "awaiting_result", "running":
		return "running", nil
	case "succeeded", "completed":
		if finishedAt == nil || finishedAt.IsZero() {
			return "", fmt.Errorf("terminal legacy Run status %q has no finished_at", status)
		}
		return "succeeded", nil
	case "failed", "needs_gpt_revision":
		if finishedAt == nil || finishedAt.IsZero() {
			return "", fmt.Errorf("terminal legacy Run status %q has no finished_at", status)
		}
		return "failed", nil
	case "cancelled", "aborted":
		if finishedAt == nil || finishedAt.IsZero() {
			return "", fmt.Errorf("terminal legacy Run status %q has no finished_at", status)
		}
		return "aborted", nil
	case "cancel_requested":
		return "", fmt.Errorf("active legacy Run status %q cannot be migrated without losing cancellation semantics", status)
	default:
		return "", fmt.Errorf("unsupported legacy Run status %q", status)
	}
}

// migrationRunRecord is intentionally private and exists only while reading
// pre-cutover run.json bytes. It is never returned by a service API and never
// participates in current state authority.
type migrationRunRecord struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"task_id"`
	ProjectID      string     `json:"project_id"`
	TrainID        string     `json:"train_id,omitempty"`
	Status         string     `json:"status"`
	AgentID        string     `json:"agent_id,omitempty"`
	SessionKey     string     `json:"session_key"`
	GatewayID      string     `json:"gateway_id"`
	BaseRevision   string     `json:"base_revision"`
	CompletionPath string     `json:"completion_path,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DispatchedAt   *time.Time `json:"dispatched_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

func decodeMigrationRun(data []byte) (migrationRunRecord, bool, error) {
	var record migrationRunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return migrationRunRecord{}, false, err
	}
	if record.ID == "" || record.TaskID == "" || record.ProjectID == "" || record.Status == "" {
		return migrationRunRecord{}, false, fmt.Errorf("invalid pre-cutover run identity")
	}
	return record, false, nil
}
