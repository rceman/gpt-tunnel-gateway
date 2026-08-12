package model

import (
	"fmt"
	"time"
)

type Run struct {
	SchemaVersion       int        `json:"schema_version"`
	ID                  string     `json:"id"`
	TaskID              string     `json:"task_id"`
	TaskSHA256          string     `json:"task_sha256"`
	TaskRevision        int        `json:"task_revision,omitempty"`
	TaskRevisionSHA256  string     `json:"task_revision_sha256,omitempty"`
	TaskRunNumber       uint64     `json:"task_run_number,omitempty"`
	ProjectID           string     `json:"project_id"`
	GatewayID           string     `json:"gateway_id"`
	SessionKey          string     `json:"session_key"`
	AgentID             string     `json:"agent_id,omitempty"`
	RequestedReasoning  string     `json:"requested_reasoning,omitempty"`
	ResolvedReasoning   string     `json:"resolved_reasoning,omitempty"`
	AgentFallback       bool       `json:"agent_fallback,omitempty"`
	AgentFallbackReason string     `json:"agent_fallback_reason,omitempty"`
	Branch              string     `json:"branch"`
	TrainID             string     `json:"train_id,omitempty"`
	LaneBranch          string     `json:"lane_branch,omitempty"`
	BaseRevision        string     `json:"base_revision"`
	HubRevision         string     `json:"hub_revision"`
	Status              string     `json:"status"`
	DispatchMessage     string     `json:"dispatch_message,omitempty"`
	DispatchExitCode    *int       `json:"dispatch_exit_code,omitempty"`
	DispatchStdout      string     `json:"dispatch_stdout,omitempty"`
	DispatchStderr      string     `json:"dispatch_stderr,omitempty"`
	CompletionPath      string     `json:"completion_path"`
	Historical          bool       `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	DispatchedAt        *time.Time `json:"dispatched_at,omitempty"`
	RepromptCount       int        `json:"reprompt_count,omitempty"`
	LastRepromptAt      *time.Time `json:"last_reprompt_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
}

type CompletionGateResult struct {
	ID             string `json:"id"`
	ExitCode       int    `json:"exit_code"`
	Execution      string `json:"execution,omitempty"`
	TreeID         string `json:"tree_id,omitempty"`
	ContractDigest string `json:"contract_digest,omitempty"`
	ReceiptDigest  string `json:"receipt_digest,omitempty"`
}

type Completion struct {
	SchemaVersion      int                    `json:"schema_version"`
	RunID              string                 `json:"run_id"`
	TaskSHA256         string                 `json:"task_sha256"`
	TaskRevision       int                    `json:"task_revision,omitempty"`
	TaskRevisionSHA256 string                 `json:"task_revision_sha256,omitempty"`
	TaskRunNumber      uint64                 `json:"task_run_number,omitempty"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	AgentFeedback      *AgentFeedback         `json:"agent_feedback,omitempty"`
}

type RepositoryProof struct {
	Branch        string   `json:"branch"`
	Head          string   `json:"head"`
	WorktreeClean bool     `json:"worktree_clean"`
	BaseAncestor  bool     `json:"base_ancestor"`
	Commits       []string `json:"commits"`
	ChangedFiles  []string `json:"changed_files"`
	DiffScope     string   `json:"diff_scope"`
}

type Report struct {
	SchemaVersion      int                    `json:"schema_version"`
	TaskID             string                 `json:"task_id"`
	RunID              string                 `json:"run_id"`
	TaskRevision       int                    `json:"task_revision,omitempty"`
	TaskRevisionSHA256 string                 `json:"task_revision_sha256,omitempty"`
	TaskRunNumber      uint64                 `json:"task_run_number,omitempty"`
	ProjectID          string                 `json:"project_id"`
	Status             string                 `json:"status"`
	Summary            string                 `json:"summary"`
	GateResults        []CompletionGateResult `json:"gate_results"`
	ServerGateResults  []CompletionGateResult `json:"server_gate_results,omitempty"`
	AcceptanceCoverage []string               `json:"acceptance_coverage"`
	Deviations         []string               `json:"deviations"`
	RemainingRisks     []string               `json:"remaining_risks"`
	AgentFeedback      *AgentFeedback         `json:"agent_feedback,omitempty"`
	Repository         RepositoryProof        `json:"repository"`
	HubCommit          string                 `json:"hub_commit,omitempty"`
	FinishedAt         time.Time              `json:"finished_at"`
}

func ValidateTaskRunCounter(v TaskRunCounter) error {
	if v.SchemaVersion != SchemaVersion {
		return fmt.Errorf("invalid task run counter schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if _, _, err := ParseTaskID(v.TaskID); err != nil {
		return fmt.Errorf("task_id: %w", err)
	}
	if v.NextRunNumber == 0 || v.NextRunNumber > MaxSafeInteger {
		return fmt.Errorf("next_run_number must be between 1 and %d", MaxSafeInteger)
	}
	return nil
}
