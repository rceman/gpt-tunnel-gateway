package service

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

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

// TrainV2IntegrateInput contains only the Train identity and optimistic Hub
// guard. Branches, SHAs, activation inputs and Git options are server-owned.
type TrainV2IntegrateInput struct {
	ProjectID string `json:"project_id"`
	TrainID   string `json:"train_id"`
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
	CurrentRun      string         `json:"current_run,omitempty"`
	ActiveTrains    []string       `json:"active_trains,omitempty"`
	AmbiguousActive bool           `json:"ambiguous_active,omitempty"`
	NextAction      string         `json:"next_action"`
}

type TrainV2TaskPacket struct {
	Task                 model.TaskAuthoring         `json:"task"`
	Train                model.TrainV2               `json:"train"`
	Item                 model.TrainV2Item           `json:"item"`
	Run                  *model.Run                  `json:"run,omitempty"`
	ProjectConfiguration model.ProjectConfiguration  `json:"project_configuration"`
	WorkflowPolicy       model.ProjectWorkflowPolicy `json:"workflow_policy"`
	RepositoryRoot       string                      `json:"repository_root"`
	Recovery             string                      `json:"recovery"`
	Text                 string                      `json:"text"`
}
