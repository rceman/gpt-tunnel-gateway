package model

import (
	"encoding/json"
	"regexp"
	"time"
)

const (
	DurableHandoffSchemaVersion = 1
	MaxOwnerSummaryFieldBytes   = 512
	MaxTechnicalEvidenceBytes   = 64 * 1024
)

var durableSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

var durableCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

var ownerIdentifierRE = regexp.MustCompile(`(?i)([a-z0-9]+-(tsk|run|adr|opr)[a-z0-9-]*|report-[a-z0-9-]+|[0-9a-f]{40,64})`)

// OwnerSummary is the concise owner-facing part of a durable handoff or
// planner report. Technical identifiers and exact proof belong in
// TechnicalEvidence instead.
type OwnerSummary struct {
	Status              string   `json:"status"`
	Goal                string   `json:"goal"`
	CurrentlyDoing      string   `json:"currently_doing"`
	WhyItMatters        string   `json:"why_it_matters"`
	CompletedSoFar      []string `json:"completed_so_far"`
	NextStep            string   `json:"next_step"`
	OwnerActionRequired *string  `json:"owner_action_required"`
}

type TaskRef struct {
	TaskID     string `json:"task_id"`
	TaskSHA256 string `json:"task_sha256"`
}

// DeliveryHandoff is the mutable lifecycle record routed from Planner to
// Delivery. The evidence payload is intentionally separate from the concise
// owner summary and is not part of bounded default projections.
type DeliveryHandoff struct {
	SchemaVersion         int             `json:"schema_version"`
	ID                    string          `json:"id"`
	ProjectID             string          `json:"project_id"`
	TaskID                string          `json:"task_id"`
	RunID                 string          `json:"run_id"`
	TaskSHA256            string          `json:"task_sha256"`
	Status                string          `json:"status"`
	OwnerSummary          OwnerSummary    `json:"owner_summary"`
	TechnicalEvidence     json.RawMessage `json:"technical_evidence"`
	CurrentReportID       string          `json:"current_report_id,omitempty"`
	SupersedesHandoffID   string          `json:"supersedes_handoff_id,omitempty"`
	SupersededByHandoffID string          `json:"superseded_by_handoff_id,omitempty"`
	PlanRevision          int             `json:"plan_revision"`
	HubRevision           string          `json:"hub_revision"`
	TaskRefs              []TaskRef       `json:"task_refs"`
	TrainRefs             []string        `json:"train_refs"`
	PlanSectionRefs       []string        `json:"plan_section_refs"`
	OperatorEventRefs     []string        `json:"operator_event_refs"`
	ExpectedRepoBase      string          `json:"expected_repo_base"`
	ExpectedRepoHead      string          `json:"expected_repo_head"`
	FirstAction           string          `json:"first_action"`
	StopBoundary          string          `json:"stop_boundary"`
	ProhibitedOperations  []string        `json:"prohibited_operations"`
	InstructionBody       string          `json:"instruction_body"`
	RoleRefs              []string        `json:"role_refs"`
	DelegationRefs        []string        `json:"delegation_refs"`
	AuthorRole            string          `json:"author_role"`
	ConsumerRole          string          `json:"consumer_role"`
	CanonicalDigest       string          `json:"canonical_digest"`
	CreatedBy             string          `json:"created_by"`
	AcknowledgedBy        string          `json:"acknowledged_by,omitempty"`
	StartedBy             string          `json:"started_by,omitempty"`
	CancelledBy           string          `json:"cancelled_by,omitempty"`
	CancelReason          string          `json:"cancel_reason,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	AcknowledgedAt        *time.Time      `json:"acknowledged_at,omitempty"`
	StartedAt             *time.Time      `json:"started_at,omitempty"`
	CancelledAt           *time.Time      `json:"cancelled_at,omitempty"`
}

// PlannerReport is immutable. Corrections are new records linked with
// SupersedesReportID; there is deliberately no update operation.
type PlannerReport struct {
	SchemaVersion      int             `json:"schema_version"`
	ID                 string          `json:"id"`
	ProjectID          string          `json:"project_id"`
	HandoffID          string          `json:"handoff_id"`
	TaskID             string          `json:"task_id"`
	RunID              string          `json:"run_id"`
	TaskSHA256         string          `json:"task_sha256"`
	ReportType         string          `json:"report_type"`
	OwnerSummary       OwnerSummary    `json:"owner_summary"`
	TechnicalEvidence  json.RawMessage `json:"technical_evidence"`
	SupersedesReportID string          `json:"supersedes_report_id,omitempty"`
	PublishedBy        string          `json:"published_by"`
	PublishedAt        time.Time       `json:"published_at"`
}

type DeliveryHandoffStatus struct {
	SchemaVersion         int          `json:"schema_version"`
	ID                    string       `json:"id"`
	ProjectID             string       `json:"project_id"`
	TaskID                string       `json:"task_id"`
	RunID                 string       `json:"run_id"`
	Status                string       `json:"status"`
	OwnerSummary          OwnerSummary `json:"owner_summary"`
	CurrentReportID       string       `json:"current_report_id,omitempty"`
	SupersedesHandoffID   string       `json:"supersedes_handoff_id,omitempty"`
	SupersededByHandoffID string       `json:"superseded_by_handoff_id,omitempty"`
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
}

type PlannerReportStatus struct {
	SchemaVersion      int          `json:"schema_version"`
	ID                 string       `json:"id"`
	ProjectID          string       `json:"project_id"`
	HandoffID          string       `json:"handoff_id"`
	TaskID             string       `json:"task_id"`
	RunID              string       `json:"run_id"`
	ReportType         string       `json:"report_type"`
	OwnerSummary       OwnerSummary `json:"owner_summary"`
	SupersedesReportID string       `json:"supersedes_report_id,omitempty"`
	PublishedBy        string       `json:"published_by"`
	PublishedAt        time.Time    `json:"published_at"`
	Status             string       `json:"status"`
}

// PlannerReportState is mutable lifecycle state for an immutable PlannerReport.
type PlannerReportState struct {
	SchemaVersion  int        `json:"schema_version"`
	ReportID       string     `json:"report_id"`
	ReportSHA256   string     `json:"report_sha256"`
	Status         string     `json:"status"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

const (
	DeliveryHandoffPending          = "pending"
	DeliveryHandoffAcknowledged     = "acknowledged"
	DeliveryHandoffInProgress       = "in_progress"
	DeliveryHandoffCompleted        = "completed"
	DeliveryHandoffBlocked          = "blocked"
	DeliveryHandoffAwaitingDecision = "awaiting_decision"
	DeliveryHandoffCancelled        = "cancelled"
	DeliveryHandoffSuperseded       = "superseded"
)

const (
	PlannerReportCompleted        = "completed"
	PlannerReportBlocked          = "blocked"
	PlannerReportDecisionRequired = "decision_required"
	PlannerReportPublished        = "published"
	PlannerReportAcknowledged     = "acknowledged_by_planner"
	PlannerReportResolved         = "resolved"
	PlannerReportSuperseded       = "superseded"
)

type blockedPlannerReportEvidence struct {
	BlockerClass               *string  `json:"blocker_class"`
	Severity                   *string  `json:"severity"`
	FailedPrecondition         *string  `json:"failed_precondition"`
	VerifiedFacts              []string `json:"verified_facts"`
	PreservationResume         *string  `json:"preservation_resume"`
	SameRunCorrectionAvailable *bool    `json:"same_run_correction_available"`
}

type decisionPlannerReportEvidence struct {
	DecisionQuestion              *string  `json:"decision_question"`
	Options                       []string `json:"options"`
	Tradeoffs                     *string  `json:"tradeoffs"`
	Recommendation                *string  `json:"recommendation"`
	DeferralConsequence           *string  `json:"deferral_consequence"`
	PreservedState                *string  `json:"preserved_state"`
	UnauthorizedChoiceImplemented *bool    `json:"unauthorized_choice_implemented"`
}

type completedPlannerReportEvidence struct {
	Terminal         *bool   `json:"terminal"`
	Reviewed         *bool   `json:"reviewed"`
	TaskSHA256       *string `json:"task_sha256"`
	RunID            *string `json:"run_id"`
	DeliveryReportID *string `json:"delivery_report_id"`
	ReviewedHead     *string `json:"reviewed_head"`
}
