package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

func workflowPolicyOutputSchema() map[string]any {
	ci := closedOutput(map[string]any{"task": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "task_merge": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire), "release": outputEnum(model.WorkflowCIModeDisabled, model.WorkflowCIModeObserve, model.WorkflowCIModeRequire)}, "task", "task_merge", "release")
	agent := closedOutput(map[string]any{"wait_for_ci": outputBoolean()}, "wait_for_ci")
	gates := outputArray(outputEnum(model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest))
	return closedOutput(map[string]any{"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "workflow_stage": outputEnum(model.WorkflowStageTransitionalMain, model.WorkflowStageDevelopActive), "integration_branch": outputEnum("main", "develop"), "agent": agent, "ci": ci, "gates": gates, "updated_by": outputString(), "updated_at": outputDateTime()}, "schema_version", "project_id", "revision", "workflow_stage", "integration_branch", "agent", "ci", "updated_by", "updated_at")
}
