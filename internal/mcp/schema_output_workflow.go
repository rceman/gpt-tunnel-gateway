package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

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
	serverGate := closedOutput(map[string]any{"id": outputEnum(model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest), "exit_code": outputInteger(), "execution": outputEnum("executed", "reused"), "tree_id": outputString(), "contract_digest": outputString(), "receipt_digest": outputString()}, "id", "exit_code")
	repository := closedOutput(map[string]any{"branch": outputString(), "head": outputString(), "worktree_clean": outputBoolean(), "base_ancestor": outputBoolean(), "commits": outputArray(outputString()), "changed_files": outputArray(outputString()), "diff_scope": outputString()}, "branch", "head", "worktree_clean", "base_ancestor", "commits", "changed_files", "diff_scope")
	feedbackCandidate := closedOutput(map[string]any{"problem": outputString(), "proposed_tool": outputString(), "expected_reuse": outputEnum("one_off", "occasional", "recurring"), "expected_saving": outputString(), "safety_boundary": outputString()}, "problem", "proposed_tool", "expected_reuse", "expected_saving", "safety_boundary")
	feedback := closedOutput(map[string]any{"summary": outputString(), "friction": outputArray(outputString()), "improvements": outputArray(outputString()), "tool_candidates": outputArray(feedbackCandidate), "none_observed": outputBoolean()}, "friction", "improvements", "tool_candidates", "none_observed")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "task_id": outputString(), "run_id": outputString(), "task_revision": outputInteger(), "task_revision_sha256": outputString(), "task_run_number": outputInteger(), "project_id": outputString(),
		"status": outputString(), "summary": outputString(), "gate_results": outputArray(gate), "acceptance_coverage": outputArray(outputString()),
		"deviations": outputArray(outputString()), "remaining_risks": outputArray(outputString()), "server_gate_results": outputArray(serverGate), "agent_feedback": feedback, "repository": repository,
		"hub_commit": outputString(), "finished_at": outputDateTime(),
	}, "schema_version", "task_id", "run_id", "project_id", "status", "summary", "gate_results", "acceptance_coverage", "deviations", "remaining_risks", "repository", "finished_at")
}
