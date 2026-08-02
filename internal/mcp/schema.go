package mcp

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"time"
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

func planStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()), "sections": outputArray(outputString()),
		"active_task_id": outputString(), "active_run_id": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
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
		"status": outputString(), "supersedes": outputString(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "sha256", "project_id", "title", "objective", "branch", "base_revision", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}

func taskStateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(), "task_sha256": outputString(),
		"status": outputString(), "superseded_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "task_id", "task_sha256", "status", "updated_at")
}

func runOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_sha256": outputString(),
		"project_id": outputString(), "gateway_id": outputString(), "branch": outputString(),
		"base_revision": outputString(), "hub_revision": outputString(), "status": outputString(),
		"dispatch_message": outputString(), "dispatch_exit_code": outputInteger(), "dispatch_stdout": outputString(), "dispatch_stderr": outputString(),
		"completion_path": outputString(), "created_at": outputDateTime(), "dispatched_at": outputDateTime(),
		"reprompt_count": outputInteger(), "last_reprompt_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_sha256", "project_id", "gateway_id", "branch", "base_revision", "hub_revision", "status", "completion_path", "created_at")
}

func reportOutputSchema() map[string]any {
	gate := closedOutput(map[string]any{"id": outputString(), "exit_code": outputInteger()}, "id", "exit_code")
	repository := closedOutput(map[string]any{"branch": outputString(), "head": outputString(), "worktree_clean": outputBoolean(), "base_ancestor": outputBoolean(), "commits": outputArray(outputString()), "changed_files": outputArray(outputString()), "diff_scope": outputString()}, "branch", "head", "worktree_clean", "base_ancestor", "commits", "changed_files", "diff_scope")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(), "run_id": outputString(), "project_id": outputString(),
		"status": outputString(), "summary": outputString(), "gate_results": outputArray(gate), "acceptance_coverage": outputArray(outputString()),
		"deviations": outputArray(outputString()), "remaining_risks": outputArray(outputString()), "repository": repository,
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
		"root": outputString(), "mirror": outputString(), "remote": outputString(), "default_branch": outputString(),
	}, "root", "mirror", "remote", "default_branch")
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
	return closedOutput(map[string]any{"task": taskOutputSchema(), "state": taskStateOutputSchema()}, "task", "state")
}

func taskPacketOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"task": taskOutputSchema(), "run": runOutputSchema(), "project": projectOutputSchema(), "plan": planOutputSchema(),
		"repository_root": outputString(), "completion_path": outputString(),
		"finalize_command": outputString(), "text": outputString(),
	}, "task", "run", "project", "plan", "repository_root", "completion_path", "finalize_command", "text")
}

func taskReadOutputSchema() map[string]any {
	inactive := closedOutput(map[string]any{
		"task": taskOutputSchema(), "state": taskStateOutputSchema(), "active_run": outputBoolean(),
	}, "task", "state", "active_run")
	return map[string]any{"type": "object", "oneOf": []any{taskPacketOutputSchema(), inactive}}
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
	"project_list":        closedOutput(map[string]any{"projects": outputArray(projectOutputSchema())}, "projects"),
	"project_read":        projectOutputSchema(),
	"project_status":      closedOutput(map[string]any{"project": projectOutputSchema(), "local": projectConfigOutputSchema(), "worktree": worktreeStatusOutputSchema(), "plan": planStatusOutputSchema(), "hub_revision": outputString(), "progress": projectProgressOutputSchema()}, "project", "local", "worktree", "plan", "hub_revision", "progress"),
	"project_register":    operationOutputSchema(),
	"plan_read":           planOutputSchema(),
	"plan_cutover":        operationOutputSchema(),
	"plan_update":         operationOutputSchema(),
	"plan_section_read":   planSectionOutputSchema(),
	"plan_section_create": operationOutputSchema(),
	"plan_section_update": operationOutputSchema(),
	"plan_section_delete": operationOutputSchema(),
	"plan_render":         planRenderOutputSchema(),
	"plan_history": closedOutput(map[string]any{"history": outputArray(closedOutput(map[string]any{
		"sha": outputString(), "date": outputString(), "author": outputString(), "subject": outputString(),
	}, "sha", "date", "author", "subject"))}, "history"),
	"adr_list":   closedOutput(map[string]any{"adrs": outputArray(adrOutputSchema())}, "adrs"),
	"adr_read":   adrOutputSchema(),
	"adr_create": operationOutputSchema(),
	"task_create": closedOutput(map[string]any{
		"task": taskOutputSchema(), "operation": operationOutputSchema(),
	}, "task", "operation"),
	"task_list": closedOutput(map[string]any{"tasks": outputArray(taskRecordOutputSchema())}, "tasks"),
	"task_read": taskReadOutputSchema(),
	"task_dispatch": closedOutput(map[string]any{
		"run": runOutputSchema(), "operation": operationOutputSchema(),
	}, "run", "operation"),
	"task_supersede": closedOutput(map[string]any{
		"task": taskOutputSchema(), "operation": operationOutputSchema(),
	}, "task", "operation"),
	"task_cancel":         operationOutputSchema(),
	"run_list":            closedOutput(map[string]any{"runs": outputArray(runOutputSchema())}, "runs"),
	"run_read":            runOutputSchema(),
	"run_status":          runOutputSchema(),
	"run_report":          reportOutputSchema(),
	"run_review_snapshot": reviewSnapshotOutputSchema(),
	"run_agent_tail":      closedOutput(map[string]any{"text": outputString()}, "text"),
	"run_resume":          runResumeOutputSchema(),
	"agent_send":          agentSendOutputSchema(),
	"agent_tail":          agentTailOutputSchema(),
	"agent_status":        agentStatusOutputSchema(),
	"run_sweep":           sweepOutputSchema(),
	"run_cancel":          operationOutputSchema(),
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
	"system_ping", "gateway_capabilities", "project_list", "project_read", "project_status",
	"project_register", "plan_read", "plan_cutover", "plan_update", "plan_section_read",
	"plan_section_create", "plan_section_update", "plan_section_delete", "plan_render", "plan_history",
	"adr_list", "adr_read", "adr_create", "task_create", "task_list", "task_read", "task_dispatch",
	"task_supersede", "task_cancel", "run_list", "run_read", "run_status", "run_report",
	"run_review_snapshot", "run_agent_tail", "run_resume", "run_sweep", "run_cancel", "git_refresh", "git_refs",
	"agent_send", "agent_tail", "agent_status",
	"git_log", "git_show", "git_tree", "git_read_file", "git_diff", "git_compare", "git_merge_base",
	"git_worktree_status", "git_worktree_diff",
}

func canonicalToolNames() []string { return append([]string{}, canonicalToolManifest...) }
func additiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: false, OpenWorldHint: true}
}
func destructiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: false, OpenWorldHint: true}
}

var toolAnnotations = func() map[string]ToolAnnotations {
	result := map[string]ToolAnnotations{}
	for _, name := range []string{
		"system_ping", "gateway_capabilities", "project_list", "project_read", "project_status",
		"plan_read", "plan_section_read", "plan_render", "plan_history", "adr_list", "adr_read", "task_list", "task_read",
		"run_list", "run_read", "run_status", "run_report",
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
	for _, name := range []string{"project_register", "adr_create", "task_create", "plan_section_create"} {
		result[name] = additiveExternalAnnotations()
	}
	for _, name := range []string{"plan_cutover", "plan_update", "plan_section_update", "plan_section_delete", "task_dispatch", "task_supersede", "task_cancel", "run_resume", "run_sweep", "run_cancel"} {
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
	if expected, ok := schema["type"].(string); ok {
		switch expected {
		case "object":
			if _, ok := value.(map[string]any); !ok {
				return fmt.Errorf("%s: expected object, got %T", path, value)
			}
		case "array":
			if _, ok := value.([]any); !ok {
				return fmt.Errorf("%s: expected array, got %T", path, value)
			}
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s: expected string, got %T", path, value)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s: expected boolean, got %T", path, value)
			}
		case "integer":
			n, ok := value.(float64)
			if !ok || math.Trunc(n) != n {
				return fmt.Errorf("%s: expected integer, got %T", path, value)
			}
		case "number":
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("%s: expected number, got %T", path, value)
			}
		default:
			return fmt.Errorf("%s: unsupported schema type %q", path, expected)
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
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range stringList(schema["required"]) {
			if _, exists := object[required]; !exists {
				return fmt.Errorf("%s: missing required property %q", path, required)
			}
		}
		for key, child := range object {
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
