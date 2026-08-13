package model

import (
	"time"
)

const (
	TrainV2SchemaVersion        = 1
	MaxTrainV2Items             = 32
	TrainV2CutoverSchemaVersion = 1
	TrainV2Planned              = "planned"
	TrainV2Running              = "running"
	TrainV2Paused               = "paused"
	TrainV2Blocked              = "blocked"
	TrainV2ReadyForIntegration  = "ready_for_integration"
	TrainV2Completed            = "completed"
	TrainV2RecoveryQuarantined  = "recovery_quarantined"
	TrainV2ItemQueued           = "queued"
	TrainV2ItemRunning          = "running"
	TrainV2ItemFinalized        = "finalized"
	TrainV2ItemReviewed         = "reviewed"
	TrainV2ItemBlocked          = "blocked"
	TrainV2StartSchemaVersion   = 1
	TrainV2StartActive          = "active"
	TrainV2AttemptSchemaVersion = 1
	TrainV2AttemptRunning       = "running"
	TrainV2AttemptSucceeded     = "succeeded"
	TrainV2AttemptFailed        = "failed"
	TrainV2AttemptAborted       = "aborted"
	TrainV2AttemptRecovered     = "recovered"
)

// TrainV2CutoverReceipt is the durable proof that the project's execution
// authority was switched once, after the legacy graph and runtime were
// reconciled. It is deliberately separate from Plan history.
type TrainV2CutoverReceipt struct {
	SchemaVersion               int       `json:"schema_version"`
	ProjectID                   string    `json:"project_id"`
	ExecutionModel              string    `json:"execution_model"`
	ConfigurationRevision       int       `json:"configuration_revision"`
	SourceHead                  string    `json:"source_head"`
	RuntimeHead                 string    `json:"runtime_head"`
	ActionSchemaRevision        int       `json:"action_schema_revision"`
	HistoricalCompatibility     string    `json:"historical_compatibility"`
	MaterializationAcknowledged bool      `json:"materialization_acknowledged"`
	PlanRetirementAcknowledged  bool      `json:"plan_retirement_acknowledged"`
	NextAction                  string    `json:"next_action"`
	UpdatedBy                   string    `json:"updated_by"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

// TrainV2Item is the immutable admission snapshot for one ready Task. It has
// no repository, branch, worktree, Agent, or session identity; those belong to
// later train-start execution state.
type TrainV2Item struct {
	Position                int                         `json:"position"`
	TaskID                  string                      `json:"task_id"`
	TaskRevision            int                         `json:"task_revision"`
	TaskRevisionSHA256      string                      `json:"task_revision_sha256"`
	Status                  string                      `json:"status"`
	AddedAt                 time.Time                   `json:"added_at"`
	Attempts                []TrainV2Attempt            `json:"attempts,omitempty"`
	ActiveAttemptNumber     uint64                      `json:"active_attempt_number,omitempty"`
	SuccessfulAttemptNumber uint64                      `json:"successful_attempt_number,omitempty"`
	Proof                   *TrainV2ImplementationProof `json:"proof,omitempty"`
	Review                  *TrainV2ItemReview          `json:"review,omitempty"`
}

// TrainV2LegacyRunRef retains an exact, immutable reference to a pre-attempt
// Run record. The path and digest are both required because Run IDs were
// historically reusable across protocol lineages.
type TrainV2LegacyRunRef struct {
	RunID        string `json:"run_id"`
	RecordSHA256 string `json:"record_sha256"`
	Path         string `json:"path"`
}

// TrainV2Attempt is scoped to one TrainItem. It is not a project-global
// entity and must never be allocated as a durable Run ID.
type TrainV2Attempt struct {
	Number            uint64               `json:"number"`
	Status            string               `json:"status"`
	AgentID           string               `json:"agent_id"`
	AirelaySessionKey string               `json:"airelay_session_key"`
	GatewayID         string               `json:"gateway_id"`
	StartHead         string               `json:"start_head"`
	StartedAt         time.Time            `json:"started_at"`
	DispatchedAt      *time.Time           `json:"dispatched_at,omitempty"`
	FinishedAt        *time.Time           `json:"finished_at,omitempty"`
	CompletionPath    string               `json:"completion_path,omitempty"`
	ReportID          string               `json:"report_id,omitempty"`
	ReviewID          string               `json:"review_id,omitempty"`
	LegacyRunRef      *TrainV2LegacyRunRef `json:"legacy_run_ref,omitempty"`
}

// TrainV2AttemptIdentity addresses an execution attempt without a project
// global execution identifier.
type TrainV2AttemptIdentity struct {
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
}

type TrainV2AttemptMigrationItem struct {
	TaskID                string              `json:"task_id"`
	AttemptNumber         uint64              `json:"attempt_number"`
	AttemptStatus         string              `json:"attempt_status"`
	LegacyRunRef          TrainV2LegacyRunRef `json:"legacy_run_ref"`
	OriginalRunJSONBase64 string              `json:"original_run_json_base64"`
}

type TrainV2AttemptMigrationReceipt struct {
	SchemaVersion int                           `json:"schema_version"`
	ProjectID     string                        `json:"project_id"`
	TrainID       string                        `json:"train_id"`
	State         string                        `json:"state"`
	HubBefore     string                        `json:"hub_before"`
	HubAfter      string                        `json:"hub_after,omitempty"`
	Items         []TrainV2AttemptMigrationItem `json:"items"`
	Reason        string                        `json:"reason"`
	CreatedAt     time.Time                     `json:"created_at"`
	UpdatedAt     time.Time                     `json:"updated_at"`
}

// RunRetirementRecord is immutable migration evidence for a pre-cutover Run
// record. It is not a runtime Run representation and is never resolved by
// task, train, or run ID alone.
type RunRetirementRecord struct {
	SchemaVersion      int    `json:"schema_version"`
	ProjectID          string `json:"project_id"`
	SourcePath         string `json:"source_path"`
	SourceSHA256       string `json:"source_sha256"`
	OriginalRunID      string `json:"original_run_id"`
	OriginalRunTaskID  string `json:"original_run_task_id"`
	OriginalRunStatus  string `json:"original_run_status"`
	OriginalRunJSONB64 string `json:"original_run_json_base64"`
}

type RunRetirementReceipt struct {
	SchemaVersion int                   `json:"schema_version"`
	ProjectID     string                `json:"project_id"`
	State         string                `json:"state"`
	HubBefore     string                `json:"hub_before"`
	HubAfter      string                `json:"hub_after"`
	Records       []RunRetirementRecord `json:"records"`
	Reason        string                `json:"reason"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

type TrainV2ImplementationProof struct {
	CheckpointHead    string                 `json:"checkpoint_head"`
	ImplementationSHA string                 `json:"implementation_sha"`
	ReportID          string                 `json:"report_id"`
	GateResults       []CompletionGateResult `json:"gate_results"`
	RecordedAt        time.Time              `json:"recorded_at"`
}

type TrainV2ItemReview struct {
	Outcome    string    `json:"outcome"`
	ReportID   string    `json:"report_id"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

type TrainV2AttemptReview struct {
	SchemaVersion int                   `json:"schema_version"`
	ID            string                `json:"id"`
	TrainID       string                `json:"train_id"`
	TaskID        string                `json:"task_id"`
	ItemPosition  int                   `json:"item_position"`
	AttemptNumber uint64                `json:"attempt_number"`
	Outcome       string                `json:"outcome"`
	ReviewedHead  string                `json:"reviewed_head"`
	Findings      []ReviewFinding       `json:"findings"`
	ScopeCoverage []ReviewScopeCoverage `json:"scope_coverage"`
	ReviewedAt    time.Time             `json:"reviewed_at"`
}

// TrainV2 is a non-running, ordered execution admission record. A later
// train/start transition owns Git and Agent execution identity.
type TrainV2 struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	ProjectID     string            `json:"project_id"`
	Revision      int               `json:"revision"`
	Items         []TrainV2Item     `json:"items"`
	Status        string            `json:"status"`
	CreatedBy     string            `json:"created_by"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	FullProof     *TrainV2FullProof `json:"full_proof,omitempty"`
}

type TrainV2FullProof struct {
	CandidateHead string                 `json:"candidate_head"`
	GateResults   []CompletionGateResult `json:"gate_results"`
	RecordedAt    time.Time              `json:"recorded_at"`
}

// TrainV2StartRecord is the portable, safe execution checkpoint. Host-local
// WorktreePath and SessionKey deliberately do not appear here.
type TrainV2StartRecord struct {
	SchemaVersion             int       `json:"schema_version"`
	ProjectID                 string    `json:"project_id"`
	TrainID                   string    `json:"train_id"`
	Status                    string    `json:"status"`
	IntegrationBranch         string    `json:"integration_branch"`
	BaseRevision              string    `json:"base_revision"`
	LaneBranch                string    `json:"lane_branch"`
	CurrentItemPosition       int       `json:"current_item_position"`
	CurrentAttemptNumber      uint64    `json:"current_attempt_number"`
	CurrentTaskID             string    `json:"current_task_id"`
	CurrentTaskRevision       int       `json:"current_task_revision"`
	CurrentTaskRevisionSHA256 string    `json:"current_task_revision_sha256"`
	StartedAt                 time.Time `json:"started_at"`
}
