package model

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

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
