package service

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2CreateInput struct {
	ProjectID string   `json:"project_id"`
	TaskIDs   []string `json:"task_ids"`
	CreatedBy string   `json:"created_by"`
	WriteOptions
}

type TrainV2AddInput struct {
	ProjectID              string   `json:"project_id"`
	TrainID                string   `json:"train_id"`
	TaskIDs                []string `json:"task_ids"`
	ExpectedRevision       int      `json:"expected_revision"`
	ExpectedRevisionSHA256 string   `json:"expected_revision_sha256,omitempty"`
	AddedBy                string   `json:"added_by"`
	WriteOptions
}

type TrainV2ListInput struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
}

type TrainV2ListResult struct {
	Trains []model.TrainV2 `json:"trains"`
}

// TrainV2StartInput contains only portable start intent. Host-local worktree
// and session bindings are resolved by the service and kept in Gateway-local
// runtime state; callers cannot provide either value.
type TrainV2StartInput struct {
	ProjectID            string `json:"project_id"`
	TrainID              string `json:"train_id"`
	StartedBy            string `json:"started_by"`
	AgentID              string `json:"agent_id,omitempty"`
	RecommendedReasoning string `json:"recommended_reasoning,omitempty"`
	WriteOptions
}

// TrainV2AdvanceInput advances the exact running Train lane to its next
// queued item. Agent/session and worktree identity are inherited from the
// successful prior Attempt; callers cannot supply a new global Run identity.
type TrainV2AdvanceInput struct {
	ProjectID string `json:"project_id"`
	TrainID   string `json:"train_id"`
	WriteOptions
}

// TrainV2CorrectionStartInput starts the exact queued correction item after a
// rejected review. The rejected review and correction item are both immutable
// identity bindings; callers cannot select a different Train or Attempt.
type TrainV2CorrectionStartInput struct {
	ProjectID                    string `json:"project_id"`
	TrainID                      string `json:"train_id"`
	RejectedItemPosition         int    `json:"rejected_item_position"`
	RejectedAttemptNumber        uint64 `json:"rejected_attempt_number"`
	RejectedReviewID             string `json:"rejected_review_id"`
	CorrectionItemPosition       int    `json:"correction_item_position"`
	CorrectionTaskID             string `json:"correction_task_id"`
	CorrectionTaskRevision       int    `json:"correction_task_revision"`
	CorrectionTaskRevisionSHA256 string `json:"correction_task_revision_sha256"`
	StartedBy                    string `json:"started_by"`
	AgentID                      string `json:"agent_id,omitempty"`
	RecommendedReasoning         string `json:"recommended_reasoning,omitempty"`
	WriteOptions
}

// TrainV2IntegrateInput contains only the Train identity and optimistic Hub
// guard. Branches, SHAs, activation inputs and Git options are server-owned.
type TrainV2IntegrateInput struct {
	ProjectID string `json:"project_id"`
	TrainID   string `json:"train_id"`
	WriteOptions
}

// TrainV2FullProofInput records the train-wide proof without performing
// integration, activation, or any mutation of the integration branch.
type TrainV2FullProofInput struct {
	ProjectID string `json:"project_id"`
	TrainID   string `json:"train_id"`
	WriteOptions
}

type TrainV2ReviewResolveInput struct {
	ProjectID             string                          `json:"project_id"`
	TrainID               string                          `json:"train_id"`
	RejectedTaskID        string                          `json:"rejected_task_id"`
	RejectedItemPosition  int                             `json:"rejected_item_position"`
	RejectedAttemptNumber uint64                          `json:"rejected_attempt_number"`
	RejectedReviewID      string                          `json:"rejected_review_id"`
	RejectedReviewedHead  string                          `json:"rejected_reviewed_head"`
	FindingIDs            []string                        `json:"finding_ids"`
	Corrections           []model.TrainV2ReviewCorrection `json:"corrections"`
	ResolvingHead         string                          `json:"resolving_head"`
	ReviewerEvidence      string                          `json:"reviewer_evidence"`
	WriteOptions
}

type TrainV2ReviewResolveResult struct {
	Train      model.TrainV2                 `json:"train"`
	Resolution model.TrainV2ReviewResolution `json:"resolution"`
	Hub        hub.TransactionResult         `json:"hub"`
}

type TrainV2ReviewBackfillInput struct {
	ProjectID string `json:"project_id"`
	TrainID   string `json:"train_id"`
	ItemStart int    `json:"item_start"`
	ItemEnd   int    `json:"item_end"`
	Apply     bool   `json:"apply"`
	WriteOptions
}

// TrainV2CutoverInput contains only the explicit migration decisions. Source,
// runtime, active-run and action-registry proofs are derived server-side.
type TrainV2CutoverInput struct {
	ProjectID                   string `json:"project_id"`
	MaterializationAcknowledged bool   `json:"materialization_acknowledged"`
	PlanRetirementAcknowledged  bool   `json:"plan_retirement_acknowledged"`
	UpdatedBy                   string `json:"updated_by"`
	WriteOptions
}

type TrainV2ProjectStatus struct {
	ExecutionModel  string         `json:"execution_model"`
	TaskCounts      map[string]int `json:"task_counts"`
	TrainCounts     map[string]int `json:"train_counts"`
	CurrentTrain    string         `json:"current_train,omitempty"`
	CurrentTask     string         `json:"current_task,omitempty"`
	CurrentAttempt  string         `json:"current_attempt,omitempty"`
	ActiveTrains    []string       `json:"active_trains,omitempty"`
	AmbiguousActive bool           `json:"ambiguous_active,omitempty"`
	NextAction      string         `json:"next_action"`
}

type TrainV2TaskPacket struct {
	Task                 model.TaskAuthoring         `json:"task"`
	Train                model.TrainV2               `json:"train"`
	Item                 model.TrainV2Item           `json:"item"`
	Attempt              *model.TrainV2Attempt       `json:"attempt,omitempty"`
	ProjectConfiguration model.ProjectConfiguration  `json:"project_configuration"`
	WorkflowPolicy       model.ProjectWorkflowPolicy `json:"workflow_policy"`
	RepositoryRoot       string                      `json:"repository_root"`
	Recovery             string                      `json:"recovery"`
	Text                 string                      `json:"text"`
}

type TaskWorkInput struct {
	ProjectID            string `json:"project_id"`
	TaskID               string `json:"task_id"`
	StartedBy            string `json:"started_by,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	RecommendedReasoning string `json:"recommended_reasoning,omitempty"`
	WriteOptions
}

type TaskWorkResult struct {
	TaskID        string `json:"task_id"`
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	AttemptNumber uint64 `json:"attempt_number"`
	AttemptStatus string `json:"attempt_status"`
	PacketPath    string `json:"packet_path"`
	WorktreePath  string `json:"worktree_path"`
	Text          string `json:"text"`
}

type TaskFinalizeInput struct {
	ProjectID          string   `json:"project_id,omitempty"`
	TaskID             string   `json:"task_id"`
	Summary            string   `json:"summary,omitempty"`
	AcceptanceCoverage []string `json:"acceptance_coverage,omitempty"`
	Deviations         []string `json:"deviations,omitempty"`
	RemainingRisks     []string `json:"remaining_risks,omitempty"`
	WriteOptions
}

type AgentRecoverInput struct {
	ProjectID     string `json:"project_id"`
	TrainID       string `json:"train_id"`
	ItemPosition  int    `json:"item_position"`
	TaskID        string `json:"task_id"`
	AttemptNumber uint64 `json:"attempt_number"`
	AgentID       string `json:"agent_id"`
}

type AgentRecoveryResult struct {
	ProjectID           string `json:"project_id"`
	TrainID             string `json:"train_id"`
	ItemPosition        int    `json:"item_position"`
	TaskID              string `json:"task_id"`
	AttemptNumber       uint64 `json:"attempt_number"`
	AgentID             string `json:"agent_id"`
	AttemptStatus       string `json:"attempt_status"`
	SessionState        string `json:"session_state"`
	ControllerReachable bool   `json:"controller_reachable"`
	Recoverable         bool   `json:"recoverable"`
	Outcome             string `json:"outcome"`
	Phase               string `json:"phase"`
	RecoveryEvent       string `json:"recovery_event,omitempty"`
	Reason              string `json:"reason,omitempty"`
}
