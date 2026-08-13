package model

import (
	"regexp"
	"time"
)

const SchemaVersion = 1

const PlanSchemaVersion = 2

const MaxDeferredReasonBytes = 1024

const MaxSafeInteger uint64 = 9007199254740991

var (
	idRE                 = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	adrIDRE              = regexp.MustCompile(`^ADR-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	projectCodeRE        = regexp.MustCompile(`^[A-Z]{3}$`)
	canonicalTaskIDRE    = regexp.MustCompile(`^([A-Z]{3})-TSK(` + OperatorJournalNumberPattern + `)$`)
	canonicalTrainV2IDRE = regexp.MustCompile(`^([A-Z]{3})-TRN(` + OperatorJournalNumberPattern + `)$`)
	canonicalRunIDRE     = regexp.MustCompile(`^([A-Z]{3}-TSK(` + OperatorJournalNumberPattern + `))-RUN(` + OperatorJournalNumberPattern + `)$`)
	canonicalADRIDRE     = regexp.MustCompile(`^([A-Z]{3})-ADR(` + OperatorJournalNumberPattern + `)$`)
	legacyTaskIDRE       = regexp.MustCompile(`^([A-Z]{3})-T([1-9][0-9]*)$`)
	legacyRunIDRE        = regexp.MustCompile(`^([A-Z]{3}-T[1-9][0-9]*)-R([1-9][0-9]*)$`)
	legacyADRIDRE        = regexp.MustCompile(`^([A-Z]{3})-A(` + OperatorJournalNumberPattern + `)$`)
	shaRE                = regexp.MustCompile(`^[0-9a-f]{40}$`)
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

const (
	TaskAuthoringSchemaVersion = 1
	TaskAuthoringPlanned       = "planned"
	TaskAuthoringReady         = "ready"
	TaskADRNoRequired          = "no_adr_required"
	TaskADRImplementsExisting  = "implements_existing"
	TaskADRRequiresNew         = "requires_new_adr"
	TaskADRSupersedesExisting  = "supersedes_existing_adr"
)

// TaskAuthoring is the train_v2 planning specification. It intentionally has
// no executable Git, worktree, lane, Agent, or session identity; those belong
// to Train/TrainItem execution records introduced by later migration slices.
type TaskAuthoring struct {
	SchemaVersion         int               `json:"schema_version"`
	ID                    string            `json:"id"`
	ProjectID             string            `json:"project_id"`
	Revision              int               `json:"revision"`
	RevisionSHA256        string            `json:"revision_sha256"`
	Title                 string            `json:"title"`
	Objective             string            `json:"objective"`
	AcceptanceCriteria    []string          `json:"acceptance_criteria"`
	Constraints           []string          `json:"constraints"`
	Priority              string            `json:"priority,omitempty"`
	Dependencies          []string          `json:"dependencies,omitempty"`
	PreparationReferences []string          `json:"preparation_references,omitempty"`
	Metadata              map[string]string `json:"metadata,omitempty"`
	ADRRelation           string            `json:"adr_relation"`
	ADRReferences         []string          `json:"adr_references,omitempty"`
	Status                string            `json:"status"`
	ReadySeal             *TaskReadySeal    `json:"ready_seal,omitempty"`
	CreatedBy             string            `json:"created_by"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

type TaskReadySeal struct {
	Revision       int       `json:"revision"`
	RevisionSHA256 string    `json:"revision_sha256"`
	ReadyBy        string    `json:"ready_by"`
	ReadyAt        time.Time `json:"ready_at"`
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
