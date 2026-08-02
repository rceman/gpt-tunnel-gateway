package model

import (
	"fmt"
	"time"
)

// AgentState is the bounded public liveness vocabulary.  It deliberately
// distinguishes an idle session from an idle session that owns active work.
const (
	AgentStateIdle                = "idle"
	AgentStateRunning             = "running"
	AgentStateWaitingForInput     = "waiting_for_input"
	AgentStateCompacting          = "compacting"
	AgentStateCompactedResuming   = "compacted_resuming"
	AgentStateCompactedIdle       = "compacted_idle"
	AgentStateCapacityBlocked     = "capacity_blocked"
	AgentStateRateLimited         = "rate_limited"
	AgentStateCompletionPending   = "completion_pending"
	AgentStateFinalizationPending = "finalization_pending"
	AgentStateStalled             = "stalled"
	AgentStateError               = "error"
	AgentStateUnknown             = "unknown"
)

var AgentStates = []string{
	AgentStateIdle,
	AgentStateRunning,
	AgentStateWaitingForInput,
	AgentStateCompacting,
	AgentStateCompactedResuming,
	AgentStateCompactedIdle,
	AgentStateCapacityBlocked,
	AgentStateRateLimited,
	AgentStateCompletionPending,
	AgentStateFinalizationPending,
	AgentStateStalled,
	AgentStateError,
	AgentStateUnknown,
}

// RunOperationalEvent is local durable operational evidence.  It contains
// only bounded identities, digests and classifications; it is never a task,
// completion, report or evidence authority.
type RunOperationalEvent struct {
	SchemaVersion     int       `json:"schema_version"`
	ID                string    `json:"id"`
	EventType         string    `json:"event_type"`
	RunID             string    `json:"run_id"`
	TaskID            string    `json:"task_id"`
	ProjectID         string    `json:"project_id"`
	CompactionEventID string    `json:"compaction_event_id,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
	TailDigest        string    `json:"tail_digest,omitempty"`
	MessageDigest     string    `json:"message_digest,omitempty"`
	ExitCode          int       `json:"exit_code"`
	ResultingState    string    `json:"resulting_state"`
}

const (
	OperationalEventSchemaVersion = 1
	EventCompactionObserved       = "compaction_observed"
	EventCompactionStarted        = "compaction_started"
	EventCompactionCompleted      = "compaction_completed"
	EventResumeSent               = "resume_sent"
	EventResumeCompleted          = "resume_completed"
	EventMeaningfulOutput         = "meaningful_output_after_resume"
	EventResumeFailed             = "resume_failed"
	EventStalledAfterCompaction   = "stalled_after_compaction"
)

func ValidateRunOperationalEvent(v RunOperationalEvent) error {
	if v.SchemaVersion != OperationalEventSchemaVersion || v.ID == "" || v.EventType == "" || v.RunID == "" || v.TaskID == "" || v.ProjectID == "" || v.OccurredAt.IsZero() || v.ResultingState == "" {
		return fmt.Errorf("invalid operational event identity")
	}
	validType := false
	for _, eventType := range []string{EventCompactionObserved, EventCompactionStarted, EventCompactionCompleted, EventResumeSent, EventResumeCompleted, EventMeaningfulOutput, EventResumeFailed, EventStalledAfterCompaction} {
		if v.EventType == eventType {
			validType = true
			break
		}
	}
	if !validType {
		return fmt.Errorf("invalid operational event type")
	}
	if v.CompactionEventID != "" && len(v.CompactionEventID) > 128 {
		return fmt.Errorf("operational event compaction id is too long")
	}
	if len(v.TailDigest) > 128 || len(v.MessageDigest) > 128 {
		return fmt.Errorf("operational event digest is too long")
	}
	if len(v.ResultingState) > 64 {
		return fmt.Errorf("operational event state is too long")
	}
	return nil
}
