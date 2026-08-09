package mcp

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func runReviewReportOutputSchema() map[string]any {
	state := closedOutput(map[string]any{
		"branch": outputString(), "base_revision": outputString(), "reviewed_head": outputString(),
		"worktree_clean": outputBoolean(), "base_ancestor": outputBoolean(),
	}, "branch", "base_revision", "reviewed_head", "worktree_clean", "base_ancestor")
	gate := closedOutput(map[string]any{"id": outputString(), "exit_code": outputInteger()}, "id", "exit_code")
	finding := closedOutput(map[string]any{"id": outputString(), "severity": outputString(), "title": outputString(), "detail": outputString()}, "id", "severity", "title", "detail")
	coverage := closedOutput(map[string]any{"surface": outputString(), "status": outputEnum("covered", "inspected_no_change", "blocked"), "detail": outputString()}, "surface", "status", "detail")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "run_id": outputString(), "task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(), "project_id": outputString(),
		"task_sha256": outputString(), "branch": outputString(), "base_revision": outputString(), "reviewed_head": outputString(),
		"outcome":          outputEnum(model.ReviewOutcomeAccepted, model.ReviewOutcomeRejected, model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive),
		"repository_state": state, "gates": outputArray(gate), "findings": outputArray(finding), "scope_coverage": outputArray(coverage),
		"changed_files": outputArray(outputString()), "unexpected_surfaces": outputArray(outputString()), "historical_compatibility": outputArray(outputString()),
		"prohibited_actions": outputArray(outputString()), "next_action": outputString(), "finished_at": outputDateTime(), "hub_commit": outputString(),
	}, "schema_version", "id", "task_id", "run_id", "project_id", "task_sha256", "branch", "base_revision", "reviewed_head", "outcome", "repository_state", "gates", "findings", "scope_coverage", "changed_files", "unexpected_surfaces", "historical_compatibility", "prohibited_actions", "next_action", "finished_at")
}

func runReviewDraftOutputSchema() map[string]any {
	state := closedOutput(map[string]any{
		"branch": outputString(), "base_revision": outputString(), "reviewed_head": outputString(),
		"worktree_clean": outputBoolean(), "base_ancestor": outputBoolean(),
	}, "branch", "base_revision", "reviewed_head", "worktree_clean", "base_ancestor")
	gate := closedOutput(map[string]any{"id": outputString(), "exit_code": outputInteger()}, "id", "exit_code")
	finding := closedOutput(map[string]any{"id": outputString(), "severity": outputString(), "title": outputString(), "detail": outputString()}, "id", "severity", "title", "detail")
	coverage := closedOutput(map[string]any{"surface": outputString(), "status": outputEnum("covered", "inspected_no_change", "blocked"), "detail": outputString()}, "surface", "status", "detail")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "run_id": outputString(), "task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(), "project_id": outputString(),
		"task_sha256": outputString(), "branch": outputString(), "base_revision": outputString(), "reviewed_head": outputString(),
		"repository_state": state, "gates": outputArray(gate), "changed_files": outputArray(outputString()),
		"outcome":  outputEnum(model.ReviewOutcomeAccepted, model.ReviewOutcomeRejected, model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive),
		"findings": outputArray(finding), "scope_coverage": outputArray(coverage), "unexpected_surfaces": outputArray(outputString()),
		"historical_compatibility": outputArray(outputString()), "prohibited_actions": outputArray(outputString()), "next_action": outputString(),
		"completed_sections": outputArray(outputString()), "draft_revision": outputInteger(), "updated_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "run_id", "project_id", "task_sha256", "branch", "base_revision", "reviewed_head", "repository_state", "gates", "changed_files", "completed_sections", "draft_revision", "updated_at")
}

func runReviewValidationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"valid": outputBoolean(), "errors": outputArray(outputString()), "draft": runReviewDraftOutputSchema()}, "valid", "errors", "draft")
}

func sweepOutputSchema() map[string]any {
	item := closedOutput(map[string]any{
		"run_id": outputString(), "action": outputString(), "status": outputString(), "error": outputString(),
	}, "run_id", "action", "status")
	return closedOutput(map[string]any{"checked": outputInteger(), "items": outputArray(item)}, "checked", "items")
}

func refOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"name": outputString(), "object_type": outputString(), "object_name": outputString(), "subject": outputString(), "committer_date": outputString(),
	}, "name", "object_type", "object_name")
}

func commitOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"sha": outputString(), "parents": outputArray(outputString()), "author_name": outputString(), "author_email": outputString(),
		"author_date": outputString(), "subject": outputString(),
	}, "sha", "parents", "author_name", "author_email", "author_date", "subject")
}

func compareOutputSchema() map[string]any {
	return closedOutput(map[string]any{"merge_base": outputString(), "left_only": outputInteger(), "right_only": outputInteger()}, "merge_base", "left_only", "right_only")
}

func sessionInputSchema() map[string]any {
	sessionID := str("Durable session identifier for info, update, or end.")
	sessionID["pattern"] = `^S-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$`
	return obj(map[string]any{
		"action":       str("Session action: start, info, update, or end."),
		"session_id":   sessionID,
		"project_id":   str("Registered project identifier for start."),
		"role":         str("Server-authorized session role."),
		"session_type": str("Session type."),
		"session_ref":  str("Optional caller reference."),
		"label":        str("Optional bounded session label."),
	}, "action")
}
