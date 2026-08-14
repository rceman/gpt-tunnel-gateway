package mcp

func projectConfigOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"remote": outputString(), "default_branch": outputString(),
	}, "remote", "default_branch")
}

func projectStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project": projectOutputSchema(), "local": projectConfigOutputSchema(), "worktree": worktreeStatusOutputSchema(), "plan": planStatusOutputSchema(), "hub_revision": outputString(), "progress": projectProgressOutputSchema(), "workflow_policy": workflowPolicyStatusOutputSchema(), "project_configuration": projectConfigurationStatusOutputSchema(),
	}, "project", "local", "worktree", "plan", "hub_revision", "progress", "workflow_policy", "project_configuration")
}

func runtimeIdentityOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"gateway_pid": outputInteger(), "running_executable_path": outputString(), "running_executable_sha256": outputString(),
		"installed_gateway_sha256": outputString(), "installed_cli_sha256": outputString(), "installed_ctl_sha256": outputString(),
		"installed_artifact_versions": map[string]any{"type": "object", "additionalProperties": outputString()},
		"artifact_set_coherent":       outputBoolean(), "running_gateway_matches_installed": outputBoolean(),
		"installed_version": outputString(), "running_version": outputString(), "version_match": outputBoolean(),
		"gateway_ready": outputBoolean(), "tunnel_pid": outputInteger(), "tunnel_ready": outputBoolean(),
		"source_sha": outputString(), "source_provenance_available": outputBoolean(), "exact_source_match": outputBoolean(),
		"provenance_reason": outputString(),
	}, "artifact_set_coherent", "running_gateway_matches_installed", "version_match", "gateway_ready", "tunnel_ready", "source_provenance_available", "exact_source_match")
}

func coreToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"call":   genericCallOutputSchema(),
		"schema": genericSchemaOutputSchema(),
		"batch":  genericBatchOutputSchema(),
		"status": closedOutput(map[string]any{
			"service": outputString(), "version": outputString(), "gateway_id": outputString(), "time": outputDateTime(),
			"project_status": projectStatusOutputSchema(), "runtime_identity": runtimeIdentityOutputSchema(),
		}, "service", "version", "gateway_id", "time", "runtime_identity"),
		"rules":   workflowPolicyOutputSchema(),
		"project": genericCallOutputSchema(),
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
		"project_status":                 projectStatusOutputSchema(),
		"project_workflow_policy_read":   workflowPolicyOutputSchema(),
		"project_workflow_policy_adopt":  closedOutput(map[string]any{"policy": workflowPolicyOutputSchema(), "operation": operationOutputSchema()}, "policy", "operation"),
		"project_workflow_policy_update": closedOutput(map[string]any{"policy": workflowPolicyOutputSchema(), "operation": operationOutputSchema()}, "policy", "operation"),
		"project_onboard":                projectOnboardingResultSchema(),
		"project_onboard_status":         projectOnboardingStatusSchema(),
		"project_onboard_recover":        projectOnboardingResultSchema(),
	}
}
