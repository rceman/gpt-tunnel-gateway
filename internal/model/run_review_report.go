package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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

func NewRunReviewReportID(runID string) string { return runID + "-REPORT" }

func ValidateReviewOutcome(value string) error {
	switch value {
	case ReviewOutcomeAccepted, ReviewOutcomeRejected, ReviewOutcomeBlocked, ReviewOutcomeInconclusive:
		return nil
	default:
		return fmt.Errorf("invalid review outcome")
	}
}

func ValidateRunReviewReportDraft(v RunReviewReportDraft) error {
	if v.SchemaVersion != RunReviewReportSchemaVersion || v.ID != NewRunReviewReportID(v.RunID) {
		return fmt.Errorf("invalid review draft identity")
	}
	if err := validateReviewIdentity(v.TaskID, v.RunID, v.ProjectID, v.TaskSHA256, v.Branch, v.BaseRevision, v.ReviewedHead); err != nil {
		return err
	}
	if err := validateReviewRevisionBinding(v.TaskID, v.RunID, v.TaskRevision, v.TaskRevisionSHA256, v.TaskRunNumber); err != nil {
		return err
	}
	if v.DraftRevision < 1 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid review draft revision")
	}
	if err := validateReviewMachine(v.Branch, v.BaseRevision, v.ReviewedHead, v.RepositoryState, v.Gates, v.ChangedFiles); err != nil {
		return err
	}
	if v.Outcome != "" {
		if err := ValidateReviewOutcome(v.Outcome); err != nil {
			return err
		}
	}
	if err := validateReviewManual(v.Findings, v.ScopeCoverage, v.UnexpectedSurfaces, v.HistoricalCompatibility, v.ProhibitedActions, v.NextAction, false); err != nil {
		return err
	}
	return validateReviewSections(v.CompletedSections, false)
}

func ValidateRunReviewReport(v RunReviewReport) error {
	if v.SchemaVersion != RunReviewReportSchemaVersion || v.ID != NewRunReviewReportID(v.RunID) {
		return fmt.Errorf("invalid review report identity")
	}
	if err := validateReviewIdentity(v.TaskID, v.RunID, v.ProjectID, v.TaskSHA256, v.Branch, v.BaseRevision, v.ReviewedHead); err != nil {
		return err
	}
	if err := validateReviewRevisionBinding(v.TaskID, v.RunID, v.TaskRevision, v.TaskRevisionSHA256, v.TaskRunNumber); err != nil {
		return err
	}
	if err := ValidateReviewOutcome(v.Outcome); err != nil {
		return err
	}
	if err := validateReviewMachine(v.Branch, v.BaseRevision, v.ReviewedHead, v.RepositoryState, v.Gates, v.ChangedFiles); err != nil {
		return err
	}
	if err := validateReviewManual(v.Findings, v.ScopeCoverage, v.UnexpectedSurfaces, v.HistoricalCompatibility, v.ProhibitedActions, v.NextAction, true); err != nil {
		return err
	}
	if v.FinishedAt.IsZero() {
		return fmt.Errorf("review report finished_at is required")
	}
	if v.HubCommit != "" {
		if err := ValidateCommitSHA(v.HubCommit); err != nil {
			return fmt.Errorf("hub_commit: %w", err)
		}
	}
	return nil
}

func validateReviewRevisionBinding(taskID, runID string, revision int, revisionHash string, runNumber uint64) error {
	if revision == 0 && revisionHash == "" && runNumber == 0 {
		return nil
	}
	if revision < 1 || !sha256RE(revisionHash) || runNumber == 0 || runNumber > MaxSafeInteger {
		return fmt.Errorf("invalid revision-aware review binding")
	}
	revisionID, err := FormatTaskRevisionID(taskID, revision)
	if err != nil {
		return err
	}
	want, err := FormatTaskRevisionRunID(revisionID, runNumber)
	if err != nil || runID != want {
		return fmt.Errorf("review run id does not match revision-aware binding")
	}
	return nil
}

func validateReviewIdentity(taskID, runID, projectID, taskHash, branch, base, head string) error {
	if err := ValidateObjectIdentifier(taskID); err != nil {
		return fmt.Errorf("task_id: %w", err)
	}
	if err := ValidateObjectIdentifier(runID); err != nil {
		return fmt.Errorf("run_id: %w", err)
	}
	if err := ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	if !sha256RE(taskHash) {
		return fmt.Errorf("invalid task_sha256")
	}
	if err := ValidateBranch(branch); err != nil {
		return fmt.Errorf("branch: %w", err)
	}
	if err := ValidateCommitSHA(base); err != nil {
		return fmt.Errorf("base_revision: %w", err)
	}
	if err := ValidateCommitSHA(head); err != nil {
		return fmt.Errorf("reviewed_head: %w", err)
	}
	return nil
}

func validateReviewMachine(branch, base, head string, state ReviewRepositoryState, gates []CompletionGateResult, changed []string) error {
	if state.Branch != branch || state.BaseRevision != base || state.ReviewedHead != head {
		return fmt.Errorf("repository_state does not match report identity")
	}
	if len(gates) > 128 || len(changed) > 1024 {
		return fmt.Errorf("review machine section bounds exceeded")
	}
	for i, gate := range gates {
		if gate.ID != fmt.Sprintf("G%d", i+1) || gate.ExitCode < 0 {
			return fmt.Errorf("invalid review gate result")
		}
	}
	previous := ""
	for _, path := range changed {
		if err := ValidateRelativePath(path); err != nil {
			return fmt.Errorf("changed_files: %w", err)
		}
		if previous != "" && previous >= path {
			return fmt.Errorf("changed_files must be sorted and unique")
		}
		previous = path
	}
	return nil
}

func validateReviewManual(findings []ReviewFinding, coverage []ReviewScopeCoverage, unexpected, historical, prohibited []string, next string, final bool) error {
	if len(findings) > MaxReviewFindings || len(coverage) > MaxReviewScopeCoverage || len(unexpected) > MaxReviewStringArrayEntries || len(historical) > MaxReviewStringArrayEntries || len(prohibited) > MaxReviewStringArrayEntries {
		return fmt.Errorf("review section bounds exceeded")
	}
	for _, finding := range findings {
		if !reviewFindingIDRE.MatchString(finding.ID) || len(finding.ID) > MaxReviewFindingIDLength || !reviewFindingSeverities[finding.Severity] || strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Detail) == "" {
			return fmt.Errorf("invalid review finding")
		}
		if err := reviewTextBounded(finding.Title, MaxReviewFindingTitleCodePoints, "review finding title"); err != nil {
			return err
		}
		if err := reviewTextBounded(finding.Detail, MaxReviewFindingDetailCodePoints, "review finding detail"); err != nil {
			return err
		}
	}
	for _, item := range coverage {
		if strings.TrimSpace(item.Surface) == "" || (item.Status != "covered" && item.Status != "inspected_no_change" && item.Status != "blocked") || strings.TrimSpace(item.Detail) == "" {
			return fmt.Errorf("invalid review scope coverage")
		}
		if err := reviewTextBounded(item.Surface, MaxReviewScopeSurfaceCodePoints, "review scope surface"); err != nil {
			return err
		}
		if err := reviewTextBounded(item.Detail, MaxReviewScopeDetailCodePoints, "review scope detail"); err != nil {
			return err
		}
	}
	for name, values := range map[string][]string{"unexpected_surfaces": unexpected, "historical_compatibility": historical, "prohibited_actions": prohibited} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("invalid %s entry", name)
			}
			if err := reviewTextBounded(value, MaxReviewStringEntryCodePoints, name+" entry"); err != nil {
				return err
			}
		}
	}
	if final && strings.TrimSpace(next) == "" {
		return fmt.Errorf("invalid next_action")
	}
	if next != "" {
		if err := reviewTextBounded(next, MaxReviewNextActionCodePoints, "next_action"); err != nil {
			return err
		}
	}
	return nil
}

func reviewTextBounded(value string, maxCodePoints int, field string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("invalid UTF-8 %s", field)
	}
	if utf8.RuneCountInString(value) > maxCodePoints {
		return fmt.Errorf("oversized %s", field)
	}
	return nil
}

func validateReviewSections(sections []string, final bool) error {
	allowed := map[string]bool{}
	for _, section := range RunReviewReportSections {
		allowed[section] = true
	}
	seen := map[string]bool{}
	for _, section := range sections {
		if !allowed[section] || seen[section] {
			return fmt.Errorf("invalid review section %q", section)
		}
		seen[section] = true
	}
	if final {
		for _, section := range RunReviewReportSections {
			if !seen[section] {
				return fmt.Errorf("missing review section %q", section)
			}
		}
	}
	return nil
}

func ParseRunReviewReportDraft(data []byte) (RunReviewReportDraft, error) {
	var out RunReviewReportDraft
	obj, err := strictJSONObject(data)
	if err != nil {
		return out, err
	}
	if err := validateReviewObjectKeys(obj, false); err != nil {
		return out, err
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return out, fmt.Errorf("encode review draft")
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return out, err
	}
	if err := ValidateRunReviewReportDraft(out); err != nil {
		return out, err
	}
	return out, nil
}

func ParseRunReviewReport(data []byte) (RunReviewReport, error) {
	var out RunReviewReport
	obj, err := strictJSONObject(data)
	if err != nil {
		return out, err
	}
	if err := validateReviewObjectKeys(obj, true); err != nil {
		return out, err
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return out, fmt.Errorf("encode review report")
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return out, err
	}
	if err := ValidateRunReviewReport(out); err != nil {
		return out, err
	}
	return out, nil
}

func validateReviewObjectKeys(obj map[string]any, final bool) error {
	allowed := map[string]bool{
		"schema_version": true, "id": true, "task_id": true, "run_id": true, "project_id": true,
		"task_sha256": true, "branch": true, "base_revision": true, "reviewed_head": true,
		"repository_state": true, "gates": true, "changed_files": true, "outcome": true,
		"findings": true, "scope_coverage": true, "unexpected_surfaces": true,
		"historical_compatibility": true, "prohibited_actions": true, "next_action": true,
		"completed_sections": true, "draft_revision": true, "updated_at": true,
		"finished_at": true, "hub_commit": true,
	}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown review report field %q", key)
		}
	}
	required := []string{"schema_version", "id", "task_id", "run_id", "project_id", "task_sha256", "branch", "base_revision", "reviewed_head", "repository_state", "gates", "changed_files"}
	if final {
		required = append(required, "outcome", "findings", "scope_coverage", "unexpected_surfaces", "historical_compatibility", "prohibited_actions", "next_action", "finished_at")
	} else {
		required = append(required, "completed_sections", "draft_revision", "updated_at")
	}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing review report field %q", key)
		}
	}
	if final {
		if _, ok := obj["completed_sections"]; ok {
			return fmt.Errorf("final review report cannot contain completed_sections")
		}
		if _, ok := obj["draft_revision"]; ok {
			return fmt.Errorf("final review report cannot contain draft_revision")
		}
		if _, ok := obj["updated_at"]; ok {
			return fmt.Errorf("final review report cannot contain updated_at")
		}
	} else {
		for _, key := range []string{"finished_at", "hub_commit"} {
			if _, ok := obj[key]; ok {
				return fmt.Errorf("draft review report cannot contain %s", key)
			}
		}
	}
	return nil
}

func CanonicalReviewSections(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
