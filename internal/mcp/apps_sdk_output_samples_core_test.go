package mcp

import (
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (f canonicalOutputFixture) canonicalCoreOutputSamples() map[string]any {
	project := f.project.(model.Project)
	local := f.local.(config.ProjectConfig)
	worktree := f.worktree.(gitx.WorktreeStatus)
	plan := f.plan.(model.Plan)
	policy := f.policy.(model.ProjectWorkflowPolicy)
	transaction := f.transaction.(hub.TransactionResult)
	return map[string]any{
		"call":                 map[string]any{"action": "project/read", "result": map[string]any{"project": f.project}, "is_error": false},
		"schema":               map[string]any{"revision": genericSchemaRevision, "path": "", "kind": "root", "domains": []string{"project"}, "actions": []map[string]any{}, "contract": map[string]any{}},
		"batch":                map[string]any{"results": []map[string]any{{"action": "project/read", "result": map[string]any{"project": f.project}, "is_error": false}}},
		"session":              map[string]any{"action": "info", "session": map[string]any{"schema_version": 1, "session_id": "S-01234567", "project_id": "project", "role": "delivery", "session_type": "chatgpt", "status": "active", "created_at": f.now, "started_at": f.now, "updated_at": f.now}},
		"system_ping":          map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.6.11", "gateway_id": "home_pc", "time": f.now},
		"gateway_capabilities": map[string]any{"gateway_id": "home_pc", "listen_addr": "127.0.0.1:8765", "projects": []string{"project"}, "hub_protocol_root": "gpt-tunnel/v1", "hub_repository_url": "git@github.com:rceman/typer.git", "hub_branch": "gpt-tunnel/home_pc", "hub_managed_root": "/tmp/state/hub/repository", "airelay_control_only": true, "generic_shell_available": false},
		"project_list":         map[string]any{"projects": []model.Project{project}}, "project_read": project,
		"project_identifiers_read":  model.ProjectIdentifiers{SchemaVersion: 1, ProjectID: "project", ProjectCode: "GTW", NextTaskNumber: 1, NextADRNumber: 1},
		"project_identifiers_adopt": map[string]any{"identifiers": model.ProjectIdentifiers{SchemaVersion: 1, ProjectID: "project", ProjectCode: "GTW", NextTaskNumber: 1, NextADRNumber: 1}, "operation": f.operation},
		"project_status":            service.ProjectStatus{Project: project, Local: local, Worktree: worktree, Plan: plan.StatusView(), HubRevision: transaction.After, WorkflowPolicy: service.ProjectWorkflowPolicyStatus{State: "adopted", Revision: 1, WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main", AgentWaitForCI: false, CI: policy.CI, Conflicts: []string{}, CorrectiveAction: "none"}},
		"project_onboard": map[string]any{
			"operation_id": "11111111-1111-4111-8111-111111111111", "project_id": "project", "state": "activated",
			"request_sha256": strings.Repeat("a", 64), "receipt_sha256": strings.Repeat("b", 64), "hub_transaction": true,
			"journal_repair_only": false, "registry_before": strings.Repeat("c", 64), "registry_after": strings.Repeat("d", 64),
			"mirror_ready": true, "recovery_status": "not_required",
		},
		"project_onboard_status": map[string]any{
			"operation_id": "11111111-1111-4111-8111-111111111111", "project_id": "project", "state": "activated",
			"request_sha256": strings.Repeat("a", 64), "receipt_sha256": strings.Repeat("b", 64), "started_at": f.now, "updated_at": f.now,
			"recovery_status": "not_required", "recovery_step": "", "hub_before": transaction.Before, "hub_after": transaction.After,
			"hub_committed": true, "registry_before": strings.Repeat("c", 64), "registry_after": strings.Repeat("d", 64),
			"registry_ready": true, "mirror_ready": true, "project_ready": true, "session_ready": true,
		},
		"project_onboard_recover": map[string]any{
			"operation_id": "11111111-1111-4111-8111-111111111111", "project_id": "project", "state": "activated",
			"request_sha256": strings.Repeat("a", 64), "receipt_sha256": strings.Repeat("b", 64), "hub_transaction": false,
			"journal_repair_only": true, "registry_before": strings.Repeat("c", 64), "registry_after": strings.Repeat("d", 64),
			"mirror_ready": true, "recovery_status": "not_required",
		},
		"project_workflow_policy_read":   f.policy,
		"project_workflow_policy_adopt":  map[string]any{"policy": f.policy, "operation": f.operation},
		"project_workflow_policy_update": map[string]any{"policy": f.policy, "operation": f.operation},
	}
}
