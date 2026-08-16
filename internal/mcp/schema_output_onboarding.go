package mcp

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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

func minimalProjectOnboardingInputSchema() map[string]any {
	projectID := str("Durable project identifier")
	projectID["pattern"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
	root := str("Absolute source repository root")
	code := str("Optional three-letter uppercase project code")
	code["pattern"] = "^[A-Z]{3}$"
	objective := str("Optional bounded initial workflow objective")
	return obj(map[string]any{"project_id": projectID, "root": root, "project_code": code, "initial_objective": objective}, "project_id", "root")
}

func minimalProjectOnboardingResultSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "project_code": outputString(), "state": outputString(), "activated": outputBoolean(),
		"repository_url": outputString(), "default_branch": outputString(), "head": outputString(), "clean": outputBoolean(),
		"agent_session_key": outputString(), "agent_ready": outputBoolean(), "next_step": outputString(), "operation_id": outputString(),
	}, "project_id", "project_code", "state", "activated", "repository_url", "default_branch", "head", "clean", "agent_session_key", "agent_ready", "next_step", "operation_id")
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
	gates := outputArray(outputEnum(model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest))
	return closedOutput(map[string]any{"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "workflow_stage": outputEnum(model.WorkflowStageTransitionalMain, model.WorkflowStageDevelopActive), "integration_branch": outputEnum("main", "develop"), "agent": agent, "ci": ci, "gates": gates, "updated_by": outputString(), "updated_at": outputDateTime()}, "schema_version", "project_id", "revision", "workflow_stage", "integration_branch", "agent", "ci", "updated_by", "updated_at")
}
