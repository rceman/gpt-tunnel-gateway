package model

import (
	"regexp"
	"time"
)

const RunReviewReportSchemaVersion = 1

const (
	MaxReviewFindings                = 256
	MaxReviewScopeCoverage           = 256
	MaxReviewStringArrayEntries      = 256
	MaxReviewFindingIDLength         = 64
	MaxReviewFindingTitleCodePoints  = 512
	MaxReviewFindingDetailCodePoints = 20000
	MaxReviewScopeSurfaceCodePoints  = 512
	MaxReviewScopeDetailCodePoints   = 20000
	MaxReviewStringEntryCodePoints   = 20000
	MaxReviewNextActionCodePoints    = 20000
)

const (
	ReviewOutcomeAccepted     = "accepted_reviewed_merge_ready"
	ReviewOutcomeRejected     = "rejected_needs_correction"
	ReviewOutcomeBlocked      = "blocked_planner_decision_required"
	ReviewOutcomeInconclusive = "inconclusive_review_required"
)

var RunReviewReportSections = []string{
	"outcome",
	"repository_state",
	"gates",
	"findings",
	"scope_coverage",
	"changed_files",
	"unexpected_surfaces",
	"historical_compatibility",
	"prohibited_actions",
	"next_action",
}

var reviewFindingIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var reviewFindingSeverities = map[string]bool{
	"critical": true,
	"high":     true,
	"medium":   true,
	"low":      true,
	"info":     true,
}

type ReviewRepositoryState struct {
	Branch        string `json:"branch"`
	BaseRevision  string `json:"base_revision"`
	ReviewedHead  string `json:"reviewed_head"`
	WorktreeClean bool   `json:"worktree_clean"`
	BaseAncestor  bool   `json:"base_ancestor"`
}

type ReviewFinding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

type ReviewScopeCoverage struct {
	Surface string `json:"surface"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
}

// RunReviewReportDraft is Gateway-local mutable authoring state. It is never
// written to the Hub and its machine fields are refreshed by the service.
type RunReviewReportDraft struct {
	SchemaVersion           int                    `json:"schema_version"`
	ID                      string                 `json:"id"`
	TaskID                  string                 `json:"task_id"`
	RunID                   string                 `json:"run_id"`
	ProjectID               string                 `json:"project_id"`
	TaskSHA256              string                 `json:"task_sha256"`
	TaskRevision            int                    `json:"task_revision,omitempty"`
	TaskRevisionSHA256      string                 `json:"task_revision_sha256,omitempty"`
	TaskRunNumber           uint64                 `json:"task_run_number,omitempty"`
	Branch                  string                 `json:"branch"`
	BaseRevision            string                 `json:"base_revision"`
	ReviewedHead            string                 `json:"reviewed_head"`
	RepositoryState         ReviewRepositoryState  `json:"repository_state"`
	Gates                   []CompletionGateResult `json:"gates"`
	ChangedFiles            []string               `json:"changed_files"`
	Outcome                 string                 `json:"outcome,omitempty"`
	Findings                []ReviewFinding        `json:"findings,omitempty"`
	ScopeCoverage           []ReviewScopeCoverage  `json:"scope_coverage,omitempty"`
	UnexpectedSurfaces      []string               `json:"unexpected_surfaces,omitempty"`
	HistoricalCompatibility []string               `json:"historical_compatibility,omitempty"`
	ProhibitedActions       []string               `json:"prohibited_actions,omitempty"`
	NextAction              string                 `json:"next_action,omitempty"`
	CompletedSections       []string               `json:"completed_sections"`
	DraftRevision           int                    `json:"draft_revision"`
	UpdatedAt               time.Time              `json:"updated_at"`
}

// RunReviewReport is the immutable Delivery authority. It is distinct from
// the Agent completion report stored at runs/<run-id>/report.json.
type RunReviewReport struct {
	SchemaVersion           int                    `json:"schema_version"`
	ID                      string                 `json:"id"`
	TaskID                  string                 `json:"task_id"`
	RunID                   string                 `json:"run_id"`
	ProjectID               string                 `json:"project_id"`
	TaskSHA256              string                 `json:"task_sha256"`
	TaskRevision            int                    `json:"task_revision,omitempty"`
	TaskRevisionSHA256      string                 `json:"task_revision_sha256,omitempty"`
	TaskRunNumber           uint64                 `json:"task_run_number,omitempty"`
	Branch                  string                 `json:"branch"`
	BaseRevision            string                 `json:"base_revision"`
	ReviewedHead            string                 `json:"reviewed_head"`
	Outcome                 string                 `json:"outcome"`
	RepositoryState         ReviewRepositoryState  `json:"repository_state"`
	Gates                   []CompletionGateResult `json:"gates"`
	Findings                []ReviewFinding        `json:"findings"`
	ScopeCoverage           []ReviewScopeCoverage  `json:"scope_coverage"`
	ChangedFiles            []string               `json:"changed_files"`
	UnexpectedSurfaces      []string               `json:"unexpected_surfaces"`
	HistoricalCompatibility []string               `json:"historical_compatibility"`
	ProhibitedActions       []string               `json:"prohibited_actions"`
	NextAction              string                 `json:"next_action"`
	FinishedAt              time.Time              `json:"finished_at"`
	HubCommit               string                 `json:"hub_commit,omitempty"`
}

type RunReviewValidation struct {
	Valid  bool                 `json:"valid"`
	Errors []string             `json:"errors"`
	Draft  RunReviewReportDraft `json:"draft"`
}

// RunReviewSummary is the bounded task-first projection of one Agent Run and
// its optional Delivery report. It deliberately contains no mutable report
// authoring state.
type RunReviewSummary struct {
	RunID            string `json:"run_id"`
	AgentStatus      string `json:"agent_status"`
	DeliveryStatus   string `json:"delivery_status"`
	DeliveryReportID string `json:"delivery_report_id,omitempty"`
	DeliveryOutcome  string `json:"delivery_outcome,omitempty"`
	ReviewedHead     string `json:"reviewed_head,omitempty"`
	Blocker          string `json:"blocker,omitempty"`
	NextAction       string `json:"next_action,omitempty"`
	HistoryOnly      bool   `json:"history_only"`
}
