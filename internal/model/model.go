package model

import (
	"fmt"
	"regexp"
	"time"
)

const SchemaVersion = 1

const PlanSchemaVersion = 2

const MaxDeferredReasonBytes = 1024

const MaxSafeInteger uint64 = 9007199254740991

var (
	idRE              = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	adrIDRE           = regexp.MustCompile(`^ADR-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	projectCodeRE     = regexp.MustCompile(`^[A-Z]{3}$`)
	canonicalTaskIDRE = regexp.MustCompile(`^([A-Z]{3})-TSK(` + OperatorJournalNumberPattern + `)$`)
	canonicalRunIDRE  = regexp.MustCompile(`^([A-Z]{3}-TSK(` + OperatorJournalNumberPattern + `))-RUN(` + OperatorJournalNumberPattern + `)$`)
	canonicalADRIDRE  = regexp.MustCompile(`^([A-Z]{3})-ADR(` + OperatorJournalNumberPattern + `)$`)
	legacyTaskIDRE    = regexp.MustCompile(`^([A-Z]{3})-T([1-9][0-9]*)$`)
	legacyRunIDRE     = regexp.MustCompile(`^([A-Z]{3}-T[1-9][0-9]*)-R([1-9][0-9]*)$`)
	legacyADRIDRE     = regexp.MustCompile(`^([A-Z]{3})-A(` + OperatorJournalNumberPattern + `)$`)
	shaRE             = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Project struct {
	SchemaVersion      int       `json:"schema_version"`
	ID                 string    `json:"id"`
	RepositoryURL      string    `json:"repository_url"`
	DefaultBranch      string    `json:"default_branch"`
	WorkflowRepository string    `json:"workflow_repository"`
	WorkflowCommit     string    `json:"workflow_commit"`
	Status             string    `json:"status"`
	ActiveTaskID       string    `json:"active_task_id,omitempty"`
	ActiveRunID        string    `json:"active_run_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ProjectIdentifiers struct {
	SchemaVersion  int    `json:"schema_version"`
	ProjectID      string `json:"project_id"`
	ProjectCode    string `json:"project_code"`
	NextTaskNumber uint64 `json:"next_task_number"`
	NextADRNumber  uint64 `json:"next_adr_number"`
}

// TaskRunCounter is stored next to one canonical task and is the only
// allocator for that task's run numbers.  It is deliberately separate from
// project-wide identifiers so concurrent tasks cannot consume each other's
// run sequence.
type TaskRunCounter struct {
	SchemaVersion int    `json:"schema_version"`
	ProjectID     string `json:"project_id"`
	TaskID        string `json:"task_id"`
	NextRunNumber uint64 `json:"next_run_number"`
}

type Plan struct {
	SchemaVersion    int                `json:"schema_version"`
	ProjectID        string             `json:"project_id"`
	Revision         int                `json:"revision"`
	Title            string             `json:"title"`
	Summary          string             `json:"summary"`
	CurrentObjective string             `json:"current_objective"`
	Queue            []string           `json:"queue"`
	Sections         []PlanSectionIndex `json:"sections"`
	ActiveTaskID     string             `json:"active_task_id,omitempty"`
	ActiveRunID      string             `json:"active_run_id,omitempty"`
	UpdatedBy        string             `json:"updated_by"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type PlanSectionIndex struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	ShortDescription string `json:"short_description"`
	Revision         int    `json:"revision"`
}

type PlanSection struct {
	SchemaVersion    int       `json:"schema_version"`
	ProjectID        string    `json:"project_id"`
	ID               string    `json:"id"`
	Revision         int       `json:"revision"`
	Title            string    `json:"title"`
	ShortDescription string    `json:"short_description"`
	Description      string    `json:"description"`
	UpdatedBy        string    `json:"updated_by"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type PlanRender struct {
	SchemaVersion    int    `json:"schema_version"`
	ProjectID        string `json:"project_id"`
	Revision         int    `json:"revision"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
	CurrentObjective string `json:"current_objective"`
	Text             string `json:"text"`
}

type PlanStatus struct {
	SchemaVersion    int       `json:"schema_version"`
	ProjectID        string    `json:"project_id"`
	Revision         int       `json:"revision"`
	Title            string    `json:"title"`
	Summary          string    `json:"summary"`
	CurrentObjective string    `json:"current_objective"`
	Queue            []string  `json:"queue"`
	Sections         []string  `json:"sections"`
	ActiveTaskID     string    `json:"active_task_id,omitempty"`
	ActiveRunID      string    `json:"active_run_id,omitempty"`
	UpdatedBy        string    `json:"updated_by"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ADR struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	Context       string    `json:"context"`
	Decision      string    `json:"decision"`
	Consequences  string    `json:"consequences"`
	Supersedes    string    `json:"supersedes,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type Task struct {
	SchemaVersion          int       `json:"schema_version"`
	ID                     string    `json:"id"`
	SHA256                 string    `json:"sha256"`
	ProjectID              string    `json:"project_id"`
	Title                  string    `json:"title"`
	Objective              string    `json:"objective"`
	Branch                 string    `json:"branch"`
	BaseRevision           string    `json:"base_revision,omitempty"`
	AcceptanceCriteria     []string  `json:"acceptance_criteria"`
	Constraints            []string  `json:"constraints"`
	RequiredGates          []string  `json:"required_gates,omitempty"`
	WorkflowPolicyRevision int       `json:"workflow_policy_revision"`
	OperationClass         string    `json:"operation_class"`
	EffectiveCIField       string    `json:"effective_ci_field"`
	EffectiveCIMode        string    `json:"effective_ci_mode"`
	WaitForCI              bool      `json:"wait_for_ci"`
	CIBlocking             bool      `json:"ci_blocking"`
	AgentMayWait           bool      `json:"agent_may_wait"`
	Status                 string    `json:"status"`
	Supersedes             string    `json:"supersedes,omitempty"`
	CreatedBy              string    `json:"created_by"`
	CreatedAt              time.Time `json:"created_at"`
}

type TaskState struct {
	SchemaVersion     int       `json:"schema_version"`
	TaskID            string    `json:"task_id"`
	TaskSHA256        string    `json:"task_sha256"`
	Status            string    `json:"status"`
	SupersededBy      string    `json:"superseded_by,omitempty"`
	ReviewedHead      string    `json:"reviewed_head,omitempty"`
	DeferredReason    string    `json:"deferred_reason,omitempty"`
	IntegrationBranch string    `json:"integration_branch,omitempty"`
	IntegrationHead   string    `json:"integration_head,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Run struct {
	SchemaVersion      int        `json:"schema_version"`
	ID                 string     `json:"id"`
	TaskID             string     `json:"task_id"`
	TaskSHA256         string     `json:"task_sha256"`
	TaskRevision       int        `json:"task_revision,omitempty"`
	TaskRevisionSHA256 string     `json:"task_revision_sha256,omitempty"`
	TaskRunNumber      uint64     `json:"task_run_number,omitempty"`
	ProjectID          string     `json:"project_id"`
	GatewayID          string     `json:"gateway_id"`
	SessionKey         string     `json:"session_key"`
	Branch             string     `json:"branch"`
	BaseRevision       string     `json:"base_revision"`
	HubRevision        string     `json:"hub_revision"`
	Status             string     `json:"status"`
	DispatchMessage    string     `json:"dispatch_message,omitempty"`
	DispatchExitCode   *int       `json:"dispatch_exit_code,omitempty"`
	DispatchStdout     string     `json:"dispatch_stdout,omitempty"`
	DispatchStderr     string     `json:"dispatch_stderr,omitempty"`
	CompletionPath     string     `json:"completion_path"`
	Historical         bool       `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	DispatchedAt       *time.Time `json:"dispatched_at,omitempty"`
	RepromptCount      int        `json:"reprompt_count,omitempty"`
	LastRepromptAt     *time.Time `json:"last_reprompt_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
}

type CompletionGateResult struct {
	ID       string `json:"id"`
	ExitCode int    `json:"exit_code"`
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
