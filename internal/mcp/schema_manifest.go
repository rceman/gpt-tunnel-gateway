package mcp

func readOnlyAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}
}

// canonicalToolManifest is the single stable inventory used to verify that
// registration, schemas, annotations, and contract tests describe the same
// MCP surface. Its length is deliberately not a protocol assertion.
var canonicalToolManifest = []string{
	"call", "schema", "batch", "session", "system_ping", "gateway_capabilities", "project_list", "project_read", "project_identifiers_read", "project_identifiers_adopt", "project_status", "project_onboard", "project_onboard_status", "project_onboard_recover", "project_workflow_policy_read", "project_workflow_policy_adopt", "project_workflow_policy_update",
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
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}
}
func idempotentMutationAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
}
func destructiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: true,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}
}

var toolAnnotations = func() map[string]ToolAnnotations {
	result := map[string]ToolAnnotations{}
	result["schema"] = readOnlyAnnotations()
	result["call"] = destructiveExternalAnnotations()
	result["batch"] = destructiveExternalAnnotations()
	result["session"] = destructiveExternalAnnotations()
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
	result["run_agent_tail"] = ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
	result["run_review_snapshot"] = ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
	result["agent_tail"] = ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
	result["agent_status"] = ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
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
	result["git_refresh"] = ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
	return result
}()
