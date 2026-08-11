package mcp

func coreToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"call":    genericCallOutputSchema(),
		"schema":  genericSchemaOutputSchema(),
		"batch":   genericBatchOutputSchema(),
		"session": sessionOutputSchema(),
		"system_ping": closedOutput(map[string]any{
			"service": outputString(), "version": outputString(), "gateway_id": outputString(), "time": outputDateTime(),
		}, "service", "version", "gateway_id", "time"),
		"gateway_capabilities": closedOutput(map[string]any{
			"gateway_id": outputString(), "listen_addr": outputString(), "projects": outputArray(outputString()),
			"hub_protocol_root": outputString(), "hub_repository_url": outputString(), "hub_branch": outputString(), "hub_managed_root": outputString(),
			"airelay_control_only": outputBoolean(), "generic_shell_available": outputBoolean(),
		}, "gateway_id", "listen_addr", "projects", "hub_protocol_root", "hub_repository_url", "hub_branch", "hub_managed_root", "airelay_control_only", "generic_shell_available"),
		"project_list":                   closedOutput(map[string]any{"projects": outputArray(projectOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "projects", "next_cursor", "has_more"),
		"project_read":                   projectOutputSchema(),
		"project_identifiers_read":       projectIdentifiersOutputSchema(),
		"project_identifiers_adopt":      closedOutput(map[string]any{"identifiers": projectIdentifiersOutputSchema(), "operation": operationOutputSchema()}, "identifiers", "operation"),
		"project_status":                 closedOutput(map[string]any{"project": projectOutputSchema(), "local": projectConfigOutputSchema(), "worktree": worktreeStatusOutputSchema(), "plan": planStatusOutputSchema(), "hub_revision": outputString(), "progress": projectProgressOutputSchema(), "workflow_policy": workflowPolicyStatusOutputSchema(), "project_configuration": projectConfigurationStatusOutputSchema()}, "project", "local", "worktree", "plan", "hub_revision", "progress", "workflow_policy", "project_configuration"),
		"project_workflow_policy_read":   workflowPolicyOutputSchema(),
		"project_workflow_policy_adopt":  closedOutput(map[string]any{"policy": workflowPolicyOutputSchema(), "operation": operationOutputSchema()}, "policy", "operation"),
		"project_workflow_policy_update": closedOutput(map[string]any{"policy": workflowPolicyOutputSchema(), "operation": operationOutputSchema()}, "policy", "operation"),
		"project_onboard":                projectOnboardingResultSchema(),
		"project_onboard_status":         projectOnboardingStatusSchema(),
		"project_onboard_recover":        projectOnboardingResultSchema(),
	}
}
