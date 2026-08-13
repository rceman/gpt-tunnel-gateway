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
	"project_register", "plan_read", "plan_cutover", "plan_update", "plan_section_read",
	"plan_section_create", "plan_section_update", "plan_section_delete", "plan_render", "plan_history",
	"adr_list", "adr_read", "adr_create", "task_create", "task_revision_list", "task_revision_read", "task_revision_status", "task_correction_create", "task_list", "task_read",
	"task_supersede", "git_refresh", "git_refs",
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
		"plan_read", "plan_section_read", "plan_render", "plan_history", "adr_list", "adr_read", "task_list", "task_read",
		"task_revision_list", "task_revision_read", "task_revision_status",
		"git_refs", "git_log", "git_show", "git_tree", "git_read_file", "git_diff", "git_compare",
		"git_merge_base", "git_worktree_status", "git_worktree_diff",
	} {
		result[name] = readOnlyAnnotations()
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
	for _, name := range []string{"project_register", "project_identifiers_adopt", "project_workflow_policy_adopt", "project_workflow_policy_update", "adr_create", "task_create", "plan_section_create"} {
		result[name] = additiveExternalAnnotations()
	}
	result["task_correction_create"] = additiveExternalAnnotations()
	result["project_onboard"] = idempotentMutationAnnotations()
	result["project_onboard_recover"] = idempotentMutationAnnotations()
	result["project_onboard_status"] = readOnlyAnnotations()
	for _, name := range []string{"plan_cutover", "plan_update", "plan_section_update", "plan_section_delete", "task_supersede"} {
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
