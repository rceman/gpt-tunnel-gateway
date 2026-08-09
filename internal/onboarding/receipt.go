package onboarding

import "regexp"

// ReceiptState identifies the durable phase represented by a prepared
// onboarding receipt. The later states are modelled here so receipts remain
// forward-readable, but O3a2 only accepts the prepared state.
type ReceiptState string

const (
	StatePrepared         ReceiptState = "prepared"
	StateHubCommitted     ReceiptState = "hub_committed"
	StateActivated        ReceiptState = "activated"
	StateRecoveryRequired ReceiptState = "recovery_required"
	StateRolledBack       ReceiptState = "rolled_back"
)

type RecoveryStatus string

const (
	RecoveryNotRequired RecoveryStatus = "not_required"
	RecoveryRequired    RecoveryStatus = "recovery_required"
)

type RecoveryStep string

const (
	RecoveryStepHubCommitted    RecoveryStep = "hub_committed"
	RecoveryStepManagedRegistry RecoveryStep = "managed_registry_activated"
	RecoveryStepManagedMirror   RecoveryStep = "managed_mirror_ready"
	RecoveryStepProjectReady    RecoveryStep = "project_ready"
	RecoveryStepSessionReady    RecoveryStep = "session_ready"
)

type RecoveryAction string

const RecoveryActionResumeActivation RecoveryAction = "resume_activation"

type Receipt struct {
	SchemaVersion      PositiveInteger     `json:"schema_version"`
	OperationID        string              `json:"operation_id"`
	RequestSHA256      string              `json:"request_sha256"`
	State              ReceiptState        `json:"state"`
	ProjectID          string              `json:"project_id"`
	RepositoryProof    RepositoryProof     `json:"repository_proof"`
	WorktreeProof      WorktreeProof       `json:"worktree_proof"`
	SessionProof       SessionProof        `json:"session_proof"`
	RegistryDigests    RegistryDigests     `json:"registry_digests"`
	Hub                HubProof            `json:"hub"`
	CreatedProject     *CreatedProject     `json:"created_project,omitempty"`
	CreatedPlan        *CreatedPlan        `json:"created_plan,omitempty"`
	CreatedIdentifiers *CreatedIdentifiers `json:"created_identifiers,omitempty"`
	MirrorProof        *MirrorProof        `json:"mirror_proof,omitempty"`
	Timestamps         Timestamps          `json:"timestamps"`
	Recovery           Recovery            `json:"recovery"`
}

type RepositoryProof struct {
	Root            string `json:"root"`
	Remote          string `json:"remote"`
	RepositoryURL   string `json:"repository_url"`
	DefaultBranch   string `json:"default_branch"`
	Branch          string `json:"branch"`
	Head            string `json:"head"`
	GatewayStateDir string `json:"gateway_state_dir"`
}

type WorktreeProof struct {
	Clean        bool   `json:"clean"`
	StatusSHA256 string `json:"status_sha256"`
}

type SessionProof struct {
	Required                  bool             `json:"required"`
	SessionKey                *string          `json:"session_key,omitempty"`
	Status                    string           `json:"status"`
	ControllerProtocolVersion *PositiveInteger `json:"controller_protocol_version,omitempty"`
}

type RegistryDigests struct {
	ManagedBeforeSHA256 string `json:"managed_before_sha256"`
	ManagedAfterSHA256  string `json:"managed_after_sha256"`
	ProjectSHA256       string `json:"project_sha256"`
	PlanSHA256          string `json:"plan_sha256"`
	IdentifiersSHA256   string `json:"identifiers_sha256"`
}

type HubProof struct {
	Before string   `json:"before"`
	After  *string  `json:"after,omitempty"`
	Paths  []string `json:"paths"`
}

type CreatedProject struct {
	ProjectID          string  `json:"project_id"`
	RepositoryURL      string  `json:"repository_url"`
	DefaultBranch      string  `json:"default_branch"`
	Status             string  `json:"status"`
	WorkflowRepository *string `json:"workflow_repository,omitempty"`
	WorkflowCommit     *string `json:"workflow_commit,omitempty"`
}

type CreatedPlan struct {
	SchemaVersion PositiveInteger `json:"schema_version"`
	ProjectID     string          `json:"project_id"`
	Revision      PositiveInteger `json:"revision"`
	Path          string          `json:"path"`
}

type CreatedIdentifiers struct {
	SchemaVersion  PositiveInteger `json:"schema_version"`
	ProjectID      string          `json:"project_id"`
	ProjectCode    string          `json:"project_code"`
	NextTaskNumber PositiveInteger `json:"next_task_number"`
	NextADRNumber  PositiveInteger `json:"next_adr_number"`
}

type MirrorProof struct {
	Path          string `json:"path"`
	RepositoryURL string `json:"repository_url"`
	Head          string `json:"head"`
}

type Timestamps struct {
	StartedAt      string  `json:"started_at"`
	UpdatedAt      string  `json:"updated_at"`
	PreparedAt     *string `json:"prepared_at,omitempty"`
	HubCommittedAt *string `json:"hub_committed_at,omitempty"`
	ActivatedAt    *string `json:"activated_at,omitempty"`
	RolledBackAt   *string `json:"rolled_back_at,omitempty"`
}

type ReceiptTimestamps = Timestamps

type Recovery struct {
	Status               RecoveryStatus  `json:"status"`
	LastCompletedState   *ReceiptState   `json:"last_completed_state,omitempty"`
	LastDurableStep      *RecoveryStep   `json:"last_durable_step,omitempty"`
	Reason               *string         `json:"reason,omitempty"`
	SafeCorrectiveAction *RecoveryAction `json:"safe_corrective_action,omitempty"`
	RolledBackAt         *string         `json:"rolled_back_at,omitempty"`
	RollbackProof        *RollbackProof  `json:"rollback_proof,omitempty"`
}

type ReceiptRecovery = Recovery

type RollbackProof struct {
	ManagedAfterSHA256 string    `json:"managed_after_sha256"`
	HubRevision        *string   `json:"hub_revision,omitempty"`
	HubPaths           *[]string `json:"hub_paths,omitempty"`
}

var (
	receiptUUIDPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	receiptSHA256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	receiptRelativePathPattern = regexp.MustCompile(`^[^/][^\x00]*$`)
)

var preparedReceiptPaths = []string{
	"gpt-tunnel/v1/projects/%s/project.json",
	"gpt-tunnel/v1/projects/%s/plan/current.json",
	"gpt-tunnel/v1/projects/%s/identifiers.json",
}
