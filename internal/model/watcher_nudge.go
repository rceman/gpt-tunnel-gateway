package model

import "time"

type WatcherNudgeReceipt struct {
	SchemaVersion int       `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	TaskID        string    `json:"task_id"`
	RunID         string    `json:"run_id"`
	Delivered     bool      `json:"delivered"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Error         string    `json:"error,omitempty"`
}
