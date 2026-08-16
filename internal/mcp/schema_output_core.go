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

func projectOperationalStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project": closedOutput(map[string]any{"project_id": outputString(), "project_code": outputString()}, "project_id", "project_code"),
		"state":   outputString(), "task_id": outputString(), "train_id": outputString(), "item_position": outputInteger(), "attempt_number": outputInteger(),
		"task_state": outputString(), "train_state": outputString(), "item_state": outputString(), "attempt_state": outputString(),
		"agent": closedOutput(map[string]any{
			"agent_id": outputString(), "expected": outputString(), "state": outputString(), "session_ready": outputBoolean(), "last_activity": outputDateTime(), "last_activity_age_seconds": outputInteger(),
		}, "expected", "state", "session_ready", "last_activity_age_seconds"),
		"operation":   closedOutput(map[string]any{"kind": outputString(), "operation_id": outputString(), "status": outputString()}, "kind", "operation_id", "status"),
		"integration": closedOutput(map[string]any{"state": outputString(), "candidate_head": outputString(), "runtime_source_sha": outputString(), "ready": outputBoolean(), "version_match": outputBoolean(), "exact_source_match": outputBoolean()}, "state", "ready", "version_match", "exact_source_match"),
		"rules":       closedOutput(map[string]any{"revision": outputInteger(), "digest": outputString(), "acknowledged": outputBoolean(), "fresh": outputBoolean()}, "revision", "digest", "acknowledged", "fresh"),
		"release_ci":  closedOutput(map[string]any{"state": outputString(), "tag": outputString(), "sha": outputString(), "status": outputString()}, "state"),
		"blocker":     outputString(), "recommended_next_action": outputString(),
	}, "project", "state", "agent", "integration", "rules", "release_ci", "recommended_next_action")
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
		"bootstrap": closedOutput(map[string]any{
			"runtime": closedOutput(map[string]any{
				"gateway_ready": outputBoolean(), "tunnel_ready": outputBoolean(), "version_match": outputBoolean(),
				"exact_source_match": outputBoolean(), "source_sha": outputString(), "running_version": outputString(),
			}, "gateway_ready", "tunnel_ready", "version_match", "exact_source_match"),
			"projects": outputArray(closedOutput(map[string]any{"project_code": outputString(), "project_id": outputString()}, "project_code", "project_id")),
			"rules":    closedOutput(map[string]any{"name": outputString(), "revision": outputString(), "content": outputString(), "digest": outputString(), "guidance": outputString()}, "name", "revision", "content", "digest", "guidance"),
		}, "runtime", "projects", "rules"),
		"session_start":  sessionStartPublicOutputSchema(),
		"session_update": sessionUpdatePublicOutputSchema(),
		"call":           genericCallOutputSchema(),
		"schema":         genericSchemaOutputSchema(),
		"batch":          genericBatchOutputSchema(),
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
		"project_onboard":                minimalProjectOnboardingResultSchema(),
		"project_onboard_status":         projectOnboardingStatusSchema(),
		"project_onboard_recover":        projectOnboardingResultSchema(),
	}
}
