package model

import (
	"fmt"
	"time"
)

type WatcherNudgeReceipt struct {
	SchemaVersion int       `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	TaskID        string    `json:"task_id"`
	TrainID       string    `json:"train_id"`
	ItemPosition  int       `json:"item_position"`
	AttemptNumber uint64    `json:"attempt_number"`
	Delivered     bool      `json:"delivered"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Error         string    `json:"error,omitempty"`
}

func ValidateWatcherNudgeReceipt(v WatcherNudgeReceipt) error {
	if v.SchemaVersion != WatcherObservationSchemaVersion {
		return fmt.Errorf("unsupported watcher nudge schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if ValidateObjectIdentifier(v.TaskID) != nil || ValidateCanonicalTaskID(v.TaskID) != nil || v.ItemPosition < 0 || v.AttemptNumber < 1 {
		return fmt.Errorf("invalid watcher nudge identity")
	}
	if v.StartedAt.IsZero() || v.FinishedAt.IsZero() {
		return fmt.Errorf("watcher nudge timestamps are required")
	}
	if len([]byte(v.Error)) > 4096 {
		return fmt.Errorf("watcher nudge error exceeds limit")
	}
	return nil
}
