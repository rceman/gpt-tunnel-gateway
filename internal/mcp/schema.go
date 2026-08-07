package mcp

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func outputString() map[string]any  { return map[string]any{"type": "string"} }
func outputBoolean() map[string]any { return map[string]any{"type": "boolean"} }
func outputInteger() map[string]any { return map[string]any{"type": "integer"} }
func outputDateTime() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}
func outputArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
func outputEnum(values ...string) map[string]any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return map[string]any{"type": "string", "enum": items}
}
func closedOutput(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func projectOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "repository_url": outputString(),
		"default_branch": outputString(), "workflow_repository": outputString(), "workflow_commit": outputString(),
		"status": outputString(), "active_task_id": outputString(), "active_run_id": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "id", "repository_url", "default_branch", "workflow_repository", "workflow_commit", "status", "created_at", "updated_at")
}

func planOutputSchema() map[string]any {
	sectionIndex := closedOutput(map[string]any{
		"id": outputString(), "title": outputString(), "short_description": outputString(), "revision": outputInteger(),
	}, "id", "title", "short_description", "revision")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()), "sections": outputArray(sectionIndex),
		"active_task_id": outputString(), "active_run_id": outputString(),
		"updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
}

func planReadProjectionOutputSchema() map[string]any {
	sectionIndex := closedOutput(map[string]any{
		"id": outputString(), "title": outputString(), "short_description": outputString(), "revision": outputInteger(),
	}, "id", "title", "short_description", "revision")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "title": outputString(),
		"summary": outputString(), "current_objective": outputString(), "active_task_id": outputString(), "active_run_id": outputString(),
		"sections": outputArray(sectionIndex), "next_cursor": outputString(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "sections")
}

func planStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()),
		"active_task_id": outputString(), "active_run_id": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "updated_by", "updated_at")
}

func planSectionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "id": outputString(), "revision": outputInteger(),
		"title": outputString(), "short_description": outputString(), "description": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "id", "revision", "title", "short_description", "description", "updated_by", "updated_at")
}

func planRenderOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "title": outputString(),
		"summary": outputString(), "current_objective": outputString(), "text": outputString(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "text")
}

func adrOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "title": outputString(),
		"status": outputString(), "context": outputString(), "decision": outputString(), "consequences": outputString(),
		"supersedes": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "title", "status", "context", "decision", "consequences", "created_at")
}

func taskOutputSchema() map[string]any {
	strings := outputArray(outputString())
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "sha256": outputString(), "project_id": outputString(),
		"title": outputString(), "objective": outputString(), "branch": outputString(), "base_revision": outputString(),
		"acceptance_criteria": strings, "constraints": strings, "required_gates": strings,
		"workflow_policy_revision": outputInteger(), "operation_class": outputString(), "effective_ci_field": outputString(), "effective_ci_mode": outputString(), "wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(), "agent_may_wait": outputBoolean(),
		"status": outputString(), "supersedes": outputString(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "sha256", "project_id", "title", "objective", "branch", "base_revision", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}

func taskRevisionOutputSchema() map[string]any {
	sha := outputString()
	sha["pattern"] = "^[0-9a-f]{64}$"
	commit := outputString()
	commit["pattern"] = "^[0-9a-f]{40}$"
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"revision_sha256": sha, "parent_task_revision": outputInteger(), "parent_task_sha256": sha,
		"project_id": outputString(), "title": outputString(), "objective": outputString(), "branch": outputString(), "base_revision": commit,
		"acceptance_criteria": outputArray(outputString()), "constraints": outputArray(outputString()), "required_gates": outputArray(outputString()),
		"workflow_policy_revision": outputInteger(), "operation_class": outputString(), "effective_ci_field": outputString(), "effective_ci_mode": outputString(),
		"wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(), "agent_may_wait": outputBoolean(), "status": outputString(),
		"source_run_id": outputString(), "source_report_id": outputString(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "revision_sha256", "project_id", "title", "objective", "branch", "base_revision", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}

func taskRevisionStatusOutputSchema() map[string]any {
	sha := outputString()
	sha["pattern"] = "^[0-9a-f]{64}$"
	commit := outputString()
	commit["pattern"] = "^[0-9a-f]{40}$"
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"revision_sha256": sha, "parent_task_revision": outputInteger(), "status": outputString(), "branch": outputString(), "base_revision": commit,
		"source_run_id": outputString(), "source_report_id": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "revision_sha256", "status", "branch", "base_revision", "created_at")
}

func taskCorrectionInputSchema() map[string]any {
	return obj(map[string]any{
		"task_id": str("Stable task identifier"), "source_revision_id": str("Exact terminal source revision"),
		"source_run_id": str("Exact terminal source run"), "source_report_id": str("Exact accepted Delivery report"),
		"title": str("Optional bounded corrected title"), "objective": str("Optional bounded corrected objective"),
		"acceptance_criteria": array(str("Acceptance criterion")), "constraints": array(str("Task constraint")),
		"required_gates": array(str("Required gate")), "created_by": str("Delivery identity"),
		"expected_hub_revision": str("Optimistic Hub revision"),
	}, "task_id", "source_revision_id", "source_run_id", "source_report_id", "created_by")
}

func taskStateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(), "task_sha256": outputString(),
		"status": outputString(), "superseded_by": outputString(), "reviewed_head": outputString(),
		"deferred_reason": outputString(), "integration_branch": outputString(), "integration_head": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "task_id", "task_sha256", "status", "updated_at")
}

func runOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_sha256": outputString(),
		"task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(),
		"project_id": outputString(), "gateway_id": outputString(), "branch": outputString(),
		"base_revision": outputString(), "hub_revision": outputString(), "status": outputString(),
		"dispatch_message": outputString(), "dispatch_exit_code": outputInteger(), "dispatch_stdout": outputString(), "dispatch_stderr": outputString(),
		"created_at": outputDateTime(), "dispatched_at": outputDateTime(),
		"reprompt_count": outputInteger(), "last_reprompt_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_sha256", "project_id", "gateway_id", "branch", "base_revision", "hub_revision", "status", "created_at")
}

func ownerSummarySchema() map[string]any {
	status := outputEnum("working", "completed", "blocked", "decision_required")
	completed := outputArray(outputString())
	completed["maxItems"] = 3
	return closedOutput(map[string]any{
		"status": status, "goal": outputString(), "currently_doing": outputString(),
		"why_it_matters": outputString(), "completed_so_far": completed,
		"next_step": outputString(), "owner_action_required": map[string]any{"anyOf": []any{outputString(), map[string]any{"type": "null"}}},
	}, "status", "goal", "currently_doing", "why_it_matters", "completed_so_far", "next_step", "owner_action_required")
}

func taskRefSchema() map[string]any {
	return closedOutput(map[string]any{"task_id": outputString(), "task_sha256": outputString()}, "task_id", "task_sha256")
}

func deliveryHandoffSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "task_id": outputString(), "run_id": outputString(), "task_sha256": outputString(),
		"status": outputString(), "owner_summary": ownerSummarySchema(), "technical_evidence": map[string]any{"type": "object"},
		"current_report_id": outputString(), "supersedes_handoff_id": outputString(), "superseded_by_handoff_id": outputString(),
		"plan_revision": outputInteger(), "hub_revision": outputString(), "task_refs": outputArray(taskRefSchema()), "train_refs": outputArray(outputString()),
		"plan_section_refs": outputArray(outputString()), "operator_event_refs": outputArray(outputString()), "expected_repo_base": outputString(), "expected_repo_head": outputString(),
		"first_action": outputString(), "stop_boundary": outputString(), "prohibited_operations": outputArray(outputString()), "instruction_body": outputString(),
		"role_refs": outputArray(outputString()), "delegation_refs": outputArray(outputString()), "author_role": outputString(), "consumer_role": outputString(),
		"canonical_digest": outputString(), "created_by": outputString(), "acknowledged_by": outputString(), "started_by": outputString(),
		"cancelled_by": outputString(), "cancel_reason": outputString(), "created_at": outputDateTime(), "updated_at": outputDateTime(),
		"acknowledged_at": outputDateTime(), "started_at": outputDateTime(), "cancelled_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "task_id", "run_id", "task_sha256", "status", "owner_summary", "technical_evidence", "plan_revision", "hub_revision", "task_refs", "train_refs", "plan_section_refs", "operator_event_refs", "expected_repo_base", "expected_repo_head", "first_action", "stop_boundary", "prohibited_operations", "instruction_body", "role_refs", "delegation_refs", "author_role", "consumer_role", "canonical_digest", "created_by", "created_at", "updated_at")
}

func deliveryHandoffStatusSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "task_id": outputString(), "run_id": outputString(),
		"status": outputString(), "owner_summary": ownerSummarySchema(), "current_report_id": outputString(), "supersedes_handoff_id": outputString(), "superseded_by_handoff_id": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "task_id", "run_id", "status", "owner_summary", "created_at", "updated_at")
}

func plannerReportSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "handoff_id": outputString(), "task_id": outputString(), "run_id": outputString(), "task_sha256": outputString(),
		"report_type": outputString(), "owner_summary": ownerSummarySchema(), "technical_evidence": map[string]any{"type": "object"}, "supersedes_report_id": outputString(),
		"published_by": outputString(), "published_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "handoff_id", "task_id", "run_id", "task_sha256", "report_type", "owner_summary", "technical_evidence", "published_by", "published_at")
}

func plannerReportStatusSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(), "handoff_id": outputString(), "task_id": outputString(), "run_id": outputString(),
		"report_type": outputString(), "owner_summary": ownerSummarySchema(), "supersedes_report_id": outputString(), "published_by": outputString(), "published_at": outputDateTime(), "status": outputString(),
	}, "schema_version", "id", "project_id", "handoff_id", "task_id", "run_id", "report_type", "owner_summary", "published_by", "published_at", "status")
}

func plannerReportStateSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "report_id": outputString(), "report_sha256": outputString(), "status": outputString(), "acknowledged_by": outputString(), "resolved_by": outputString(),
		"updated_at": outputDateTime(), "acknowledged_at": outputDateTime(), "resolved_at": outputDateTime(),
	}, "schema_version", "report_id", "report_sha256", "status", "updated_at")
}

func nullableInputString(desc string) map[string]any {
	return map[string]any{"anyOf": []any{str(desc), map[string]any{"type": "null"}}}
}

func ownerSummaryInputSchema() map[string]any {
	status := str("Owner-facing status")
	status["enum"] = []string{"working", "completed", "blocked", "decision_required"}
	completed := array(str("Completed item"))
	completed["maxItems"] = 3
	return obj(map[string]any{
		"status": status, "goal": str("Owner-facing goal"), "currently_doing": str("Current owner-facing activity"),
		"why_it_matters": str("Owner-facing importance"), "completed_so_far": completed, "next_step": str("Next owner-facing step"),
		"owner_action_required": nullableInputString("Optional owner action"),
	}, "status", "goal", "currently_doing", "why_it_matters", "completed_so_far", "next_step", "owner_action_required")
}

func taskRefsInputSchema() map[string]any {
	return array(obj(map[string]any{"task_id": str("Task identifier"), "task_sha256": str("Exact durable task hash")}, "task_id", "task_sha256"))
}

func deliveryHandoffCreateSchema() map[string]any {
	return obj(map[string]any{
		"project_id": str("Project identifier"), "task_id": str("Primary task identifier"), "run_id": str("Primary run identifier"), "task_sha256": str("Exact task hash"),
		"owner_summary": ownerSummaryInputSchema(), "technical_evidence": map[string]any{"type": "object"}, "plan_revision": integer("Current plan revision", 1, 1000000), "hub_revision": str("Current Hub commit"),
		"task_refs": taskRefsInputSchema(), "train_refs": array(str("Train reference")), "plan_section_refs": array(str("Plan section reference")), "operator_event_refs": array(str("Operator event reference")),
		"expected_repo_base": str("Optional expected repository base"), "expected_repo_head": str("Optional expected repository head"), "first_action": str("First action"), "stop_boundary": str("Stop boundary"),
		"prohibited_operations": array(str("Prohibited operation")), "instruction_body": str("Instruction body"), "role_refs": array(str("Role reference")), "delegation_refs": array(str("Delegation reference")), "created_by": str("Creator"),
		"expected_hub_revision": str("Optimistic Hub revision"),
	}, "project_id", "task_id", "run_id", "owner_summary", "technical_evidence", "plan_revision", "hub_revision", "task_refs", "train_refs", "first_action", "stop_boundary", "prohibited_operations", "instruction_body", "created_by")
}

func deliveryHandoffSupersedeSchema() map[string]any {
	return obj(map[string]any{
		"handoff_id": str("Existing handoff identifier"), "owner_summary": ownerSummaryInputSchema(), "technical_evidence": map[string]any{"type": "object"}, "plan_revision": integer("Current plan revision", 1, 1000000), "hub_revision": str("Current Hub commit"),
		"task_refs": taskRefsInputSchema(), "train_refs": array(str("Train reference")), "plan_section_refs": array(str("Plan section reference")), "operator_event_refs": array(str("Operator event reference")),
		"expected_repo_base": str("Optional expected repository base"), "expected_repo_head": str("Optional expected repository head"), "first_action": str("First action"), "stop_boundary": str("Stop boundary"),
		"prohibited_operations": array(str("Prohibited operation")), "instruction_body": str("Instruction body"), "role_refs": array(str("Role reference")), "delegation_refs": array(str("Delegation reference")), "created_by": str("Creator"), "expected_hub_revision": str("Optimistic Hub revision"),
	}, "handoff_id", "owner_summary", "technical_evidence", "plan_revision", "hub_revision", "task_refs", "train_refs", "first_action", "stop_boundary", "prohibited_operations", "instruction_body", "created_by")
}

func plannerReportInputSchema() map[string]any {
	reportType := str("Report type")
	reportType["enum"] = []string{"completed", "blocked", "decision_required"}
	return obj(map[string]any{
		"schema_version": integer("Report schema version", 1, 1), "id": str("Optional report identifier"), "report_type": reportType, "owner_summary": ownerSummaryInputSchema(),
		"technical_evidence": map[string]any{"type": "object"}, "supersedes_report_id": str("Optional corrected report identifier"), "published_by": str("Publisher"), "published_at": str("Optional publication timestamp"),
	}, "report_type", "owner_summary", "technical_evidence", "published_by")
}

func taskPacketRunOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_sha256": outputString(),
		"task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(),
		"project_id": outputString(), "gateway_id": outputString(), "branch": outputString(),
		"base_revision": outputString(), "hub_revision": outputString(), "status": outputString(),
		"dispatch_message": outputString(), "dispatch_exit_code": outputInteger(), "dispatch_stdout": outputString(), "dispatch_stderr": outputString(),
		"created_at": outputDateTime(), "dispatched_at": outputDateTime(),
		"reprompt_count": outputInteger(), "last_reprompt_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_sha256", "project_id", "gateway_id", "branch", "base_revision", "hub_revision", "status", "created_at")
}

func reportOutputSchema() map[string]any {
	gate := closedOutput(map[string]any{"id": outputString(), "exit_code": outputInteger()}, "id", "exit_code")
	repository := closedOutput(map[string]any{"branch": outputString(), "head": outputString(), "worktree_clean": outputBoolean(), "base_ancestor": outputBoolean(), "commits": outputArray(outputString()), "changed_files": outputArray(outputString()), "diff_scope": outputString()}, "branch", "head", "worktree_clean", "base_ancestor", "commits", "changed_files", "diff_scope")
	feedbackCandidate := closedOutput(map[string]any{"problem": outputString(), "proposed_tool": outputString(), "expected_reuse": outputEnum("one_off", "occasional", "recurring"), "expected_saving": outputString(), "safety_boundary": outputString()}, "problem", "proposed_tool", "expected_reuse", "expected_saving", "safety_boundary")
	feedback := closedOutput(map[string]any{"summary": outputString(), "friction": outputArray(outputString()), "improvements": outputArray(outputString()), "tool_candidates": outputArray(feedbackCandidate), "none_observed": outputBoolean()}, "friction", "improvements", "tool_candidates", "none_observed")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(), "run_id": outputString(), "task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(), "project_id": outputString(),
		"status": outputString(), "summary": outputString(), "gate_results": outputArray(gate), "acceptance_coverage": outputArray(outputString()),
		"deviations": outputArray(outputString()), "remaining_risks": outputArray(outputString()), "agent_feedback": feedback, "repository": repository,
		"hub_commit": outputString(), "finished_at": outputDateTime(),
	}, "schema_version", "task_id", "run_id", "project_id", "status", "summary", "gate_results", "acceptance_coverage", "deviations", "remaining_risks", "repository", "finished_at")
}

func transactionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"before": outputString(), "after": outputString(), "remote": outputString(), "branch": outputString(), "paths": outputArray(outputString()),
	}, "before", "after", "remote", "branch", "paths")
}

func operationOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"hub": transactionOutputSchema(), "project_id": outputString(), "task_id": outputString(), "run_id": outputString(), "status": outputString(),
	}, "hub", "status")
}

func operatorJournalEventOutputSchema() map[string]any {
	contentItem := operatorJournalBoundedString(outputString(), model.MaxOperatorContentItemBytes)
	content := closedOutput(map[string]any{
		"decisions": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "commitments": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "facts": operatorJournalArray(contentItem, model.MaxOperatorContentItems),
		"assumptions": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "blockers": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "unresolved": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "next_actions": operatorJournalArray(contentItem, model.MaxOperatorContentItems),
	}, "decisions", "commitments", "facts", "assumptions", "blockers", "unresolved", "next_actions")
	references := closedOutput(map[string]any{
		"plan_sections": operatorJournalArray(operatorJournalObjectID(outputString()), model.MaxOperatorReferenceItems), "adrs": operatorJournalArray(operatorJournalADR(outputString()), model.MaxOperatorReferenceItems), "tasks": operatorJournalArray(operatorJournalObjectID(outputString()), model.MaxOperatorReferenceItems),
		"runs": operatorJournalArray(operatorJournalObjectID(outputString()), model.MaxOperatorReferenceItems), "commits": operatorJournalArray(operatorJournalCommit(outputString()), model.MaxOperatorReferenceItems), "identities": operatorJournalArray(operatorJournalBoundedString(outputString(), model.MaxOperatorContentItemBytes), model.MaxOperatorReferenceItems),
	}, "plan_sections", "adrs", "tasks", "runs", "commits", "identities")
	sessionID := operatorJournalNullableString(outputString())
	kind := outputEnum("user_talk", "reasoning_summary", "task_plan", "task_review", "operation", "checkpoint", "correction")
	event := closedOutput(map[string]any{
		"schema_version": func() map[string]any {
			value := outputInteger()
			value["const"] = float64(model.OperatorJournalSchemaVersion)
			return value
		}(), "id": operatorJournalEventID(outputString()), "project_id": operatorJournalProjectID(outputString()), "session_id": sessionID,
		"kind": kind, "summary": operatorJournalBoundedString(outputString(), model.MaxOperatorSummaryBytes), "content": content, "references": references,
		"supersedes_event_id": operatorJournalEventID(outputString()), "actor": operatorJournalBoundedString(outputString(), model.MaxOperatorActorBytes), "occurred_at": outputDateTime(), "recorded_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "session_id", "kind", "summary", "content", "references", "actor", "occurred_at", "recorded_at")
	event["allOf"] = []any{
		map[string]any{"if": map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "correction"}}, "required": []any{"kind"}}, "then": map[string]any{"required": []any{"supersedes_event_id"}}},
		map[string]any{"if": map[string]any{"required": []any{"supersedes_event_id"}}, "then": map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "correction"}}}},
	}
	return event
}

func operatorJournalWriteOutputSchema() map[string]any {
	return closedOutput(map[string]any{"event": operatorJournalEventOutputSchema(), "operation": operationOutputSchema()}, "event", "operation")
}

func operatorJournalHistoryOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": operatorJournalProjectID(outputString()), "events": operatorJournalArray(operatorJournalEventOutputSchema(), model.MaxOperatorHistoryLimit), "hub_revision": outputString(),
		"has_more": outputBoolean(), "next_after_event_id": operatorJournalEventID(outputString()),
	}, "project_id", "events", "hub_revision", "has_more")
}

func projectIdentifiersOutputSchema() map[string]any {
	schemaVersion := outputInteger()
	schemaVersion["const"] = float64(1)
	projectID := outputString()
	projectID["pattern"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
	projectID["minLength"] = 1
	projectID["maxLength"] = 64
	projectCode := outputString()
	projectCode["pattern"] = "^[A-Z]{3}$"
	number := map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}
	return closedOutput(map[string]any{
		"schema_version": schemaVersion, "project_id": projectID, "project_code": projectCode,
		"next_task_number": number, "next_adr_number": number,
	}, "schema_version", "project_id", "project_code", "next_task_number", "next_adr_number")
}

func agentSendOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "delivered": outputBoolean(), "exit_code": outputInteger(),
		"stdout": outputString(), "stderr": outputString(), "started_at": outputDateTime(), "finished_at": outputDateTime(), "error": outputString(),
	}, "project_id", "delivered", "exit_code", "stdout", "stderr", "started_at", "finished_at")
}

func agentTailOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "text": outputString(), "lines": outputInteger(), "skip": outputInteger(),
	}, "project_id", "text", "lines", "skip")
}

func agentStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "state": outputEnum("idle", "running", "waiting_for_input", "compacting", "compacted_resuming", "compacted_idle", "capacity_blocked", "rate_limited", "completion_pending", "finalization_pending", "stalled", "error", "unknown"), "controller_reachable": outputBoolean(),
		"airelay_version": outputString(), "protocol_version": outputString(), "capacity_warnings": outputArray(outputString()),
		"exit_code": outputInteger(), "error": outputString(),
	}, "project_id", "state", "controller_reachable", "capacity_warnings", "exit_code")
}

func runResumeOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"run_id": outputString(), "compaction_event_id": outputString(), "state": outputEnum("compacted_resuming", "error"),
		"sent": outputBoolean(), "exit_code": outputInteger(), "controller_reachable": outputBoolean(), "message_digest": outputString(), "error": outputString(),
	}, "run_id", "compaction_event_id", "state", "sent", "exit_code", "controller_reachable", "message_digest")
}

func reviewSnapshotOutputSchema() map[string]any {
	timeField := outputDateTime()
	run := closedOutput(map[string]any{"id": outputString(), "task_id": outputString(), "project_id": outputString(), "status": outputString(), "branch": outputString(), "base_revision": outputString(), "created_at": timeField, "dispatched_at": timeField, "finished_at": timeField}, "id", "task_id", "project_id", "status", "branch", "base_revision", "created_at")
	task := closedOutput(map[string]any{"id": outputString(), "sha256": outputString(), "title": outputString(), "objective": outputString(), "branch": outputString(), "base_revision": outputString(), "acceptance_criteria": outputArray(outputString()), "constraints": outputArray(outputString()), "required_gates": outputArray(outputString()), "created_by": outputString(), "created_at": timeField, "task_state_status": outputString()}, "id", "sha256", "title", "objective", "branch", "base_revision", "acceptance_criteria", "constraints", "required_gates", "created_by", "created_at", "task_state_status")
	gate := closedOutput(map[string]any{"id": outputString(), "exit_code": outputInteger()}, "id", "exit_code")
	report := closedOutput(map[string]any{"available": outputBoolean(), "error": outputString(), "status": outputString(), "summary": outputString(), "repository_head": outputString(), "repository_branch": outputString(), "repository_clean": outputBoolean(), "commits": outputArray(outputString()), "changed_files": outputArray(outputString()), "gate_results": outputArray(gate), "acceptance_coverage": outputArray(outputString()), "deviations": outputArray(outputString()), "remaining_risks": outputArray(outputString()), "finished_at": timeField, "hub_commit": outputString()}, "available")
	evidence := closedOutput(map[string]any{"available": outputBoolean(), "error": outputString(), "head": outputString(), "branch": outputString(), "worktree_clean": outputBoolean(), "notes": outputArray(outputString()), "recorded_at": timeField}, "available")
	worktree := closedOutput(map[string]any{"branch": outputString(), "head": outputString(), "upstream": outputString(), "ahead": outputInteger(), "behind": outputInteger(), "clean": outputBoolean()}, "branch", "head", "ahead", "behind", "clean")
	compare := closedOutput(map[string]any{"merge_base": outputString(), "left_only": outputInteger(), "right_only": outputInteger(), "error": outputString()}, "left_only", "right_only")
	repo := closedOutput(map[string]any{"refresh_attempted": outputBoolean(), "refresh_succeeded": outputBoolean(), "refresh_error": outputString(), "default_branch": outputString(), "default_head": outputString(), "default_head_error": outputString(), "task_branch": outputString(), "task_branch_published": outputBoolean(), "task_branch_head": outputString(), "task_branch_error": outputString(), "worktree": worktree, "worktree_error": outputString(), "evidence_head_reachable": outputBoolean(), "evidence_head_error": outputString(), "base_to_evidence": compare, "default_to_evidence": compare, "changed_files": outputArray(outputString()), "changed_files_error": outputString(), "diff_stat": outputString(), "diff_stat_error": outputString()}, "refresh_attempted", "refresh_succeeded", "default_branch", "task_branch", "task_branch_published", "worktree", "evidence_head_reachable", "base_to_evidence", "default_to_evidence", "changed_files")
	check := closedOutput(map[string]any{"id": outputString(), "severity": outputString(), "status": outputString(), "detail": outputString()}, "id", "severity", "status", "detail")
	return closedOutput(map[string]any{"schema_version": outputInteger(), "run": run, "task": task, "report": report, "evidence": evidence, "repository": repo, "checks": outputArray(check), "review_state": outputString(), "next_action": outputString()}, "schema_version", "run", "task", "report", "evidence", "repository", "checks", "review_state", "next_action")
}

func projectConfigOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"remote": outputString(), "default_branch": outputString(),
	}, "remote", "default_branch")
}

func onboardingRequestSchema() map[string]any {
	positive := integer("JSON Schema positive integer", 1, 9007199254740991)
	requestSchemaVersion := integer("Onboarding request schema version", 1, 1)
	requestSchemaVersion["const"] = 1
	planSchemaVersion := integer("Initial workflow-v2 plan schema version", 2, 2)
	planSchemaVersion["const"] = 2
	sha := str("40-character Git revision")
	sha["pattern"] = "^[0-9a-f]{40}$"
	projectID := str("Project identifier")
	projectID["pattern"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
	branch := str("Default branch")
	branch["pattern"] = "^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$"
	remote := str("Configured remote")
	remote["pattern"] = "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"
	code := str("Three-letter uppercase project code")
	code["pattern"] = "^[A-Z]{3}$"
	session := str("Airelay session key")
	session["pattern"] = "^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$"
	section := obj(map[string]any{
		"id": str("Section identifier"), "title": str("Section title"), "short_description": str("Short description"), "revision": positive,
	}, "id", "title", "short_description", "revision")
	initialPlan := obj(map[string]any{
		"schema_version": planSchemaVersion, "project_id": projectID, "revision": positive, "title": str("Plan title"), "summary": str("Plan summary"),
		"current_objective": str("Current objective"), "queue": array(str("Queue item")), "sections": array(section), "updated_by": str("Updater"), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
	airelay := obj(map[string]any{"session_required": outputBoolean(), "session_key": session}, "session_required")
	workflow := obj(map[string]any{"repository": str("Workflow repository"), "commit": sha}, "repository", "commit")
	return obj(map[string]any{
		"schema_version": requestSchemaVersion, "project_id": projectID, "root": str("Source repository root"), "remote": remote, "repository_url": str("Repository URL"),
		"default_branch": branch, "airelay": airelay, "project_code": code, "gateway_state_dir": str("Gateway state directory"),
		"workflow": workflow, "initial_plan": initialPlan, "expected_hub_revision": sha,
	}, "schema_version", "project_id", "root", "remote", "repository_url", "default_branch", "airelay", "project_code", "gateway_state_dir", "initial_plan", "expected_hub_revision")
}

func projectOnboardingInputSchema() map[string]any {
	operationID := str("Canonical onboarding operation UUID")
	operationID["pattern"] = "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
	return obj(map[string]any{"operation_id": operationID, "request": onboardingRequestSchema()}, "operation_id", "request")
}

func projectOnboardingResultSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "project_id": outputString(), "state": outputEnum("prepared", "hub_committed", "recovery_required", "activated"),
		"request_sha256": outputString(), "receipt_sha256": outputString(), "hub_transaction": outputBoolean(), "journal_repair_only": outputBoolean(),
		"registry_before": outputString(), "registry_after": outputString(), "mirror_ready": outputBoolean(), "recovery_status": outputString(),
	}, "operation_id", "project_id", "state", "request_sha256", "receipt_sha256", "hub_transaction", "journal_repair_only", "registry_before", "registry_after", "mirror_ready", "recovery_status")
}

func projectOnboardingStatusSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "project_id": outputString(), "state": outputEnum("prepared", "hub_committed", "recovery_required", "activated"), "request_sha256": outputString(), "receipt_sha256": outputString(),
		"started_at": outputDateTime(), "updated_at": outputDateTime(), "recovery_status": outputString(), "recovery_step": outputString(), "hub_before": outputString(), "hub_after": outputString(), "hub_committed": outputBoolean(),
		"registry_before": outputString(), "registry_after": outputString(), "registry_ready": outputBoolean(), "mirror_ready": outputBoolean(), "project_ready": outputBoolean(), "session_ready": outputBoolean(),
	}, "operation_id", "project_id", "state", "request_sha256", "receipt_sha256", "started_at", "updated_at", "recovery_status", "recovery_step", "hub_before", "hub_after", "hub_committed", "registry_before", "registry_after", "registry_ready", "mirror_ready", "project_ready", "session_ready")
}

func workflowPolicyOutputSchema() map[string]any {
	ci := closedOutput(map[string]any{"task": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "task_merge": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "release": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire)}, "task", "task_merge", "release")
	agent := closedOutput(map[string]any{"wait_for_ci": outputBoolean()}, "wait_for_ci")
	return closedOutput(map[string]any{"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "workflow_stage": outputEnum(model.WorkflowStageTransitionalMain, model.WorkflowStageDevelopActive), "integration_branch": outputEnum("main", "develop"), "agent": agent, "ci": ci, "updated_by": outputString(), "updated_at": outputDateTime()}, "schema_version", "project_id", "revision", "workflow_stage", "integration_branch", "agent", "ci", "updated_by", "updated_at")
}

func workflowPolicyStatusOutputSchema() map[string]any {
	ci := closedOutput(map[string]any{"task": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "task_merge": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "release": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire)}, "task", "task_merge", "release")
	return closedOutput(map[string]any{"state": outputEnum("adopted", "missing", "invalid"), "revision": outputInteger(), "workflow_stage": outputString(), "integration_branch": outputString(), "agent_wait_for_ci": outputBoolean(), "ci": ci, "active_operation_class": outputString(), "active_ci_mode": outputString(), "ci_blocking": outputBoolean(), "conflicts": outputArray(outputString()), "corrective_action": outputString()}, "state", "revision", "workflow_stage", "integration_branch", "agent_wait_for_ci", "ci", "active_operation_class", "active_ci_mode", "ci_blocking", "conflicts", "corrective_action")
}

func projectStatusOutputSchema() map[string]any {
	baselineProperties := map[string]any{
		"project": projectOutputSchema(), "local": projectConfigOutputSchema(), "worktree": worktreeStatusOutputSchema(), "plan": planStatusOutputSchema(),
		"hub_revision": outputString(), "progress": projectProgressOutputSchema(), "workflow_policy": workflowPolicyStatusOutputSchema(), "status_token": outputString(),
	}
	baseline := closedOutput(baselineProperties, "project", "local", "worktree", "plan", "hub_revision", "progress", "workflow_policy", "status_token")
	changes := closedOutput(baselineProperties)
	delta := closedOutput(map[string]any{
		"project_id": outputString(), "changed": outputBoolean(), "status_token": outputString(), "changed_components": outputArray(outputString()), "changes": changes,
	}, "project_id", "changed", "status_token")
	return map[string]any{"oneOf": []any{baseline, delta}}
}

func projectProgressOutputSchema() map[string]any {
	task := closedOutput(map[string]any{"id": outputString(), "title": outputString(), "status": outputString(), "created_at": outputDateTime()}, "id", "title", "status", "created_at")
	run := closedOutput(map[string]any{"id": outputString(), "task_id": outputString(), "status": outputString(), "branch": outputString(), "base_revision": outputString(), "created_at": outputDateTime(), "dispatched_at": outputDateTime(), "finished_at": outputDateTime()}, "id", "task_id", "status", "branch", "base_revision", "created_at")
	return closedOutput(map[string]any{
		"latest_task": task, "latest_run": run,
		"agent_state":          outputEnum("idle", "running", "waiting_for_input", "compacting", "compacted_resuming", "compacted_idle", "capacity_blocked", "rate_limited", "completion_pending", "finalization_pending", "stalled", "error", "unknown"),
		"controller_reachable": outputBoolean(), "airelay_version": outputString(), "protocol_version": outputString(), "capacity_warnings": outputArray(outputString()), "exit_code": outputInteger(), "error": outputString(),
		"last_meaningful_activity": outputDateTime(), "last_meaningful_activity_age_seconds": outputInteger(), "tail": outputString(), "blocker_classification": outputString(), "recommended_next_action": outputString(), "component_errors": outputArray(outputString()),
	}, "agent_state", "controller_reachable", "capacity_warnings", "exit_code", "last_meaningful_activity_age_seconds", "tail", "blocker_classification", "recommended_next_action", "component_errors")
}

func worktreeStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"branch": outputString(), "head": outputString(), "upstream": outputString(), "ahead": outputInteger(), "behind": outputInteger(),
		"porcelain": outputString(), "clean": outputBoolean(),
	}, "branch", "head", "ahead", "behind", "porcelain", "clean")
}

func taskRecordOutputSchema() map[string]any {
	summary := runReviewSummaryOutputSchema()
	return closedOutput(map[string]any{"task": taskOutputSchema(), "state": taskStateOutputSchema(), "current_revision": taskRevisionOutputSchema(), "run_summaries": outputArray(summary), "workflow_policy": workflowPolicyOutputSchema()}, "task", "state")
}

func runReviewSummaryOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"run_id": outputString(), "agent_status": outputString(), "delivery_status": outputString(),
		"delivery_report_id": outputString(), "delivery_outcome": outputEnum(model.ReviewOutcomeAccepted, model.ReviewOutcomeRejected, model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive),
		"reviewed_head": outputString(), "blocker": outputString(), "next_action": outputString(), "history_only": outputBoolean(),
	}, "run_id", "agent_status", "delivery_status", "history_only")
}

func taskPacketOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"task": taskOutputSchema(), "current_revision": taskRevisionOutputSchema(), "run": taskPacketRunOutputSchema(), "project": projectOutputSchema(), "plan": planOutputSchema(), "workflow_policy": workflowPolicyOutputSchema(),
		"run_summaries":    outputArray(runReviewSummaryOutputSchema()),
		"repository_root":  outputString(),
		"finalize_command": outputString(), "text": outputString(),
	}, "task", "run", "project", "plan", "workflow_policy", "repository_root", "finalize_command", "text")
}

func taskReadOutputSchema() map[string]any {
	inactive := closedOutput(map[string]any{
		"task": taskOutputSchema(), "state": taskStateOutputSchema(), "current_revision": taskRevisionOutputSchema(), "run_summaries": outputArray(runReviewSummaryOutputSchema()), "workflow_policy": workflowPolicyOutputSchema(), "active_run": outputBoolean(),
	}, "task", "state", "active_run")
	return map[string]any{"type": "object", "oneOf": []any{taskPacketOutputSchema(), inactive}}
}

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

var toolOutputSchemas = map[string]map[string]any{
	"system_ping": closedOutput(map[string]any{
		"service": outputString(), "version": outputString(), "gateway_id": outputString(), "time": outputDateTime(),
	}, "service", "version", "gateway_id", "time"),
	"gateway_capabilities": closedOutput(map[string]any{
		"gateway_id": outputString(), "listen_addr": outputString(), "projects": outputArray(outputString()),
		"hub_protocol_root": outputString(), "hub_repository_url": outputString(), "hub_branch": outputString(), "hub_managed_root": outputString(),
		"airelay_control_only": outputBoolean(), "generic_shell_available": outputBoolean(),
	}, "gateway_id", "listen_addr", "projects", "hub_protocol_root", "hub_repository_url", "hub_branch", "hub_managed_root", "airelay_control_only", "generic_shell_available"),
	"project_list":                   closedOutput(map[string]any{"projects": outputArray(projectOutputSchema())}, "projects"),
	"project_read":                   projectOutputSchema(),
	"project_identifiers_read":       projectIdentifiersOutputSchema(),
	"project_identifiers_adopt":      closedOutput(map[string]any{"identifiers": projectIdentifiersOutputSchema(), "operation": operationOutputSchema()}, "identifiers", "operation"),
	"project_status":                 projectStatusOutputSchema(),
	"project_workflow_policy_read":   workflowPolicyOutputSchema(),
	"project_workflow_policy_adopt":  closedOutput(map[string]any{"policy": workflowPolicyOutputSchema(), "operation": operationOutputSchema()}, "policy", "operation"),
	"project_workflow_policy_update": closedOutput(map[string]any{"policy": workflowPolicyOutputSchema(), "operation": operationOutputSchema()}, "policy", "operation"),
	"delivery_handoff_publish":       closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
	"delivery_handoff_read":          deliveryHandoffSchema(),
	"delivery_handoff_status":        deliveryHandoffStatusSchema(),
	"delivery_handoff_list":          closedOutput(map[string]any{"handoffs": outputArray(deliveryHandoffStatusSchema())}, "handoffs"),
	"delivery_handoff_acknowledge":   closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
	"delivery_handoff_next":          closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
	"delivery_handoff_cancel":        closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
	"delivery_handoff_supersede":     closedOutput(map[string]any{"handoff": deliveryHandoffSchema(), "operation": operationOutputSchema()}, "handoff", "operation"),
	"planner_report_publish":         closedOutput(map[string]any{"report": plannerReportSchema(), "operation": operationOutputSchema()}, "report", "operation"),
	"planner_report_read":            plannerReportSchema(),
	"planner_report_status":          plannerReportStatusSchema(),
	"planner_report_list":            closedOutput(map[string]any{"reports": outputArray(plannerReportStatusSchema())}, "reports"),
	"planner_report_acknowledge":     closedOutput(map[string]any{"state": plannerReportStateSchema(), "operation": operationOutputSchema()}, "state", "operation"),
	"planner_report_next":            closedOutput(map[string]any{"state": plannerReportStateSchema(), "operation": operationOutputSchema()}, "state", "operation"),
	"project_register":               operationOutputSchema(),
	"project_onboard":                projectOnboardingResultSchema(),
	"project_onboard_status":         projectOnboardingStatusSchema(),
	"project_onboard_recover":        projectOnboardingResultSchema(),
	"plan_read":                      planReadProjectionOutputSchema(),
	"plan_cutover":                   operationOutputSchema(),
	"plan_update":                    operationOutputSchema(),
	"plan_section_read":              planSectionOutputSchema(),
	"plan_section_create":            operationOutputSchema(),
	"plan_section_update":            operationOutputSchema(),
	"plan_section_delete":            operationOutputSchema(),
	"plan_render":                    planRenderOutputSchema(),
	"plan_history": closedOutput(map[string]any{"history": outputArray(closedOutput(map[string]any{
		"sha": outputString(), "date": outputString(), "author": outputString(), "subject": outputString(),
	}, "sha", "date", "author", "subject"))}, "history"),
	"adr_list":   closedOutput(map[string]any{"adrs": outputArray(adrOutputSchema())}, "adrs"),
	"adr_read":   adrOutputSchema(),
	"adr_create": operationOutputSchema(),
	"task_create": closedOutput(map[string]any{
		"task": taskOutputSchema(), "operation": operationOutputSchema(),
	}, "task", "operation"),
	"task_revision_list":                closedOutput(map[string]any{"revisions": outputArray(taskRevisionOutputSchema())}, "revisions"),
	"task_revision_read":                taskRevisionOutputSchema(),
	"task_revision_status":              taskRevisionStatusOutputSchema(),
	"task_correction_create":            closedOutput(map[string]any{"revision": taskRevisionOutputSchema(), "operation": operationOutputSchema()}, "revision", "operation"),
	"task_list":                         closedOutput(map[string]any{"tasks": outputArray(taskRecordOutputSchema())}, "tasks"),
	"task_read":                         taskReadOutputSchema(),
	"task_review_report_start":          runReviewDraftOutputSchema(),
	"task_review_report_section_update": runReviewDraftOutputSchema(),
	"task_review_report_validate":       runReviewValidationOutputSchema(),
	"task_review_report_finalize":       closedOutput(map[string]any{"report": runReviewReportOutputSchema(), "operation": operationOutputSchema()}, "report", "operation"),
	"task_report_read":                  runReviewReportOutputSchema(),
	"task_dispatch": closedOutput(map[string]any{
		"run": runOutputSchema(), "operation": operationOutputSchema(),
	}, "run", "operation"),
	"task_supersede": closedOutput(map[string]any{
		"task": taskOutputSchema(), "operation": operationOutputSchema(),
	}, "task", "operation"),
	"task_cancel":                        operationOutputSchema(),
	"task_mark_merge_ready":              operationOutputSchema(),
	"task_defer":                         operationOutputSchema(),
	"task_mark_merged":                   operationOutputSchema(),
	"run_list":                           closedOutput(map[string]any{"runs": outputArray(runOutputSchema())}, "runs"),
	"run_read":                           runOutputSchema(),
	"run_status":                         runOutputSchema(),
	"run_report":                         reportOutputSchema(),
	"run_review_snapshot":                reviewSnapshotOutputSchema(),
	"run_agent_tail":                     closedOutput(map[string]any{"text": outputString()}, "text"),
	"run_resume":                         runResumeOutputSchema(),
	"agent_send":                         agentSendOutputSchema(),
	"agent_tail":                         agentTailOutputSchema(),
	"agent_status":                       agentStatusOutputSchema(),
	"run_sweep":                          sweepOutputSchema(),
	"run_cancel":                         operationOutputSchema(),
	"run_cancel_acknowledge_no_mutation": operationOutputSchema(),
	"operator_record":                    operatorJournalWriteOutputSchema(),
	"operator_history":                   operatorJournalHistoryOutputSchema(),
	"operator_checkpoint":                operatorJournalWriteOutputSchema(),
	"git_refresh": closedOutput(map[string]any{
		"project_id": outputString(), "refreshed": outputBoolean(),
	}, "project_id", "refreshed"),
	"git_refs":            closedOutput(map[string]any{"refs": outputArray(refOutputSchema())}, "refs"),
	"git_log":             closedOutput(map[string]any{"commits": outputArray(commitOutputSchema())}, "commits"),
	"git_show":            closedOutput(map[string]any{"text": outputString()}, "text"),
	"git_tree":            closedOutput(map[string]any{"paths": outputArray(outputString())}, "paths"),
	"git_read_file":       closedOutput(map[string]any{"path": outputString(), "revision": outputString(), "content": outputString()}, "path", "revision", "content"),
	"git_diff":            closedOutput(map[string]any{"diff": outputString()}, "diff"),
	"git_compare":         compareOutputSchema(),
	"git_merge_base":      closedOutput(map[string]any{"merge_base": outputString()}, "merge_base"),
	"git_worktree_status": worktreeStatusOutputSchema(),
	"git_worktree_diff": closedOutput(map[string]any{
		"diff": outputString(), "staged": outputBoolean(),
	}, "diff", "staged"),
}

func readOnlyAnnotations() ToolAnnotations {
	return ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: false}
}

// canonicalToolManifest is the single stable inventory used to verify that
// registration, schemas, annotations, and contract tests describe the same
// MCP surface. Its length is deliberately not a protocol assertion.
var canonicalToolManifest = []string{
	"system_ping", "gateway_capabilities", "project_list", "project_read", "project_identifiers_read", "project_identifiers_adopt", "project_status", "project_onboard", "project_onboard_status", "project_onboard_recover", "project_workflow_policy_read", "project_workflow_policy_adopt", "project_workflow_policy_update",
	"delivery_handoff_publish", "delivery_handoff_read", "delivery_handoff_status", "delivery_handoff_list", "delivery_handoff_acknowledge", "delivery_handoff_next", "delivery_handoff_cancel", "delivery_handoff_supersede", "planner_report_publish", "planner_report_read", "planner_report_status", "planner_report_list", "planner_report_acknowledge", "planner_report_next",
	"project_register", "plan_read", "plan_cutover", "plan_update", "plan_section_read",
	"plan_section_create", "plan_section_update", "plan_section_delete", "plan_render", "plan_history",
	"adr_list", "adr_read", "adr_create", "task_create", "task_revision_list", "task_revision_read", "task_revision_status", "task_correction_create", "task_list", "task_read", "task_review_report_start", "task_review_report_section_update", "task_review_report_validate", "task_review_report_finalize", "task_report_read", "task_dispatch",
	"task_supersede", "task_cancel", "task_mark_merge_ready", "task_defer", "task_mark_merged", "run_list", "run_read", "run_status", "run_report",
	"run_review_snapshot", "run_agent_tail", "run_resume", "run_sweep", "run_cancel", "run_cancel_acknowledge_no_mutation", "git_refresh", "git_refs",
	"agent_send", "agent_tail", "agent_status",
	"operator_record", "operator_history", "operator_checkpoint",
	"git_log", "git_show", "git_tree", "git_read_file", "git_diff", "git_compare", "git_merge_base",
	"git_worktree_status", "git_worktree_diff",
}

func canonicalToolNames() []string { return append([]string{}, canonicalToolManifest...) }
func additiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: true}
}
func idempotentMutationAnnotations() ToolAnnotations {
	return ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
}
func destructiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: false, OpenWorldHint: true}
}

var toolAnnotations = func() map[string]ToolAnnotations {
	result := map[string]ToolAnnotations{}
	for _, name := range []string{
		"system_ping", "gateway_capabilities", "project_list", "project_read", "project_identifiers_read", "project_status", "project_workflow_policy_read",
		"delivery_handoff_read", "delivery_handoff_status", "delivery_handoff_list", "planner_report_read", "planner_report_status", "planner_report_list",
		"plan_read", "plan_section_read", "plan_render", "plan_history", "adr_list", "adr_read", "task_list", "task_read",
		"task_revision_list", "task_revision_read", "task_revision_status",
		"run_list", "run_read", "run_status", "run_report", "task_review_report_validate", "task_report_read",
		"git_refs", "git_log", "git_show", "git_tree", "git_read_file", "git_diff", "git_compare",
		"git_merge_base", "git_worktree_status", "git_worktree_diff",
	} {
		result[name] = readOnlyAnnotations()
	}
	result["run_agent_tail"] = ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
	result["run_review_snapshot"] = ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
	result["agent_tail"] = ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
	result["agent_status"] = ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
	result["agent_send"] = additiveExternalAnnotations()
	result["operator_record"] = additiveExternalAnnotations()
	result["operator_checkpoint"] = additiveExternalAnnotations()
	result["operator_history"] = readOnlyAnnotations()
	for _, name := range []string{"project_register", "project_identifiers_adopt", "project_workflow_policy_adopt", "project_workflow_policy_update", "adr_create", "task_create", "plan_section_create", "delivery_handoff_publish", "planner_report_publish"} {
		result[name] = additiveExternalAnnotations()
	}
	result["task_correction_create"] = additiveExternalAnnotations()
	for _, name := range []string{"delivery_handoff_acknowledge", "delivery_handoff_next", "delivery_handoff_cancel", "delivery_handoff_supersede", "planner_report_acknowledge", "planner_report_next"} {
		result[name] = destructiveExternalAnnotations()
	}
	result["project_onboard"] = idempotentMutationAnnotations()
	result["project_onboard_recover"] = idempotentMutationAnnotations()
	result["project_onboard_status"] = readOnlyAnnotations()
	result["task_review_report_start"] = additiveExternalAnnotations()
	result["task_review_report_section_update"] = additiveExternalAnnotations()
	result["task_review_report_finalize"] = destructiveExternalAnnotations()
	for _, name := range []string{"plan_cutover", "plan_update", "plan_section_update", "plan_section_delete", "task_dispatch", "task_supersede", "task_cancel", "task_mark_merge_ready", "task_defer", "task_mark_merged", "run_resume", "run_sweep", "run_cancel", "run_cancel_acknowledge_no_mutation"} {
		result[name] = destructiveExternalAnnotations()
	}
	result["git_refresh"] = ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true}
	return result
}()

func validateOutputValue(schema map[string]any, value any) error {
	return validateSchemaValue(schema, value, "$")
}

func validateSchemaValue(schema map[string]any, value any, path string) error {
	if schema == nil {
		return fmt.Errorf("%s: missing schema", path)
	}
	if options, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, candidate := range options {
			candidateSchema, ok := candidate.(map[string]any)
			if ok && validateSchemaValue(candidateSchema, value, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: no anyOf schema matched", path)
		}
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		for _, candidate := range allOf {
			candidateSchema, ok := candidate.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: invalid allOf schema", path)
			}
			if err := validateSchemaValue(candidateSchema, value, path); err != nil {
				return err
			}
		}
	}
	if condition, ok := schema["if"].(map[string]any); ok && validateSchemaValue(condition, value, path) == nil {
		thenSchema, ok := schema["then"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: invalid then schema", path)
		}
		if err := validateSchemaValue(thenSchema, value, path); err != nil {
			return err
		}
	}
	if expected, ok := schema["type"]; ok && !schemaTypeMatches(expected, value) {
		return fmt.Errorf("%s: value has wrong type %T", path, value)
	}
	if number, ok := value.(float64); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%s: number is not finite", path)
		}
		if minimum, ok := schemaNumber(schema["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s: number is below minimum", path)
		}
		if maximum, ok := schemaNumber(schema["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s: number exceeds maximum", path)
		}
	}
	if options, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, candidate := range options {
			candidateSchema, ok := candidate.(map[string]any)
			if ok && validateSchemaValue(candidateSchema, value, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: expected exactly one matching output shape, got %d", path, matches)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value is outside enum", path)
		}
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("%s: value does not match const", path)
	}
	if text, ok := value.(string); ok {
		if pattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil || !matched {
				return fmt.Errorf("%s: string does not match pattern", path)
			}
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return fmt.Errorf("%s: invalid date-time", path)
			}
		}
		if minimum, ok := schemaNumber(schema["minLength"]); ok && float64(utf8.RuneCountInString(text)) < minimum {
			return fmt.Errorf("%s: string is shorter than minLength", path)
		}
		if maximum, ok := schemaNumber(schema["maxLength"]); ok && float64(utf8.RuneCountInString(text)) > maximum {
			return fmt.Errorf("%s: string exceeds maxLength", path)
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range stringList(schema["required"]) {
			if _, exists := object[required]; !exists {
				return fmt.Errorf("%s: missing required property %q", path, required)
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := object[key]
			childSchemaRaw, exists := properties[key]
			if !exists {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: unknown property %q", path, key)
				}
				continue
			}
			childSchema, ok := childSchemaRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: invalid child schema", path, key)
			}
			if err := validateSchemaValue(childSchema, child, path+"."+key); err != nil {
				return err
			}
		}
	}
	if values, ok := value.([]any); ok {
		if minimum, ok := schemaNumber(schema["minItems"]); ok && float64(len(values)) < minimum {
			return fmt.Errorf("%s: array has fewer than minItems", path)
		}
		if maximum, ok := schemaNumber(schema["maxItems"]); ok && float64(len(values)) > maximum {
			return fmt.Errorf("%s: array exceeds maxItems", path)
		}
		if itemRaw, exists := schema["items"]; exists {
			itemSchema, ok := itemRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: invalid item schema", path)
			}
			for index, item := range values {
				if err := validateSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func schemaTypeMatches(typeValue any, value any) bool {
	if types, ok := typeValue.([]any); ok {
		for _, candidate := range types {
			if schemaTypeMatches(candidate, value) {
				return true
			}
		}
		return false
	}
	typeName, ok := typeValue.(string)
	if !ok {
		return false
	}
	switch typeName {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
	case "number":
		n, ok := value.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return false
	}
}

func schemaNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func stringList(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
