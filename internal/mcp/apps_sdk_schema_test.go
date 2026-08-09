package mcp

import (
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestToolsListSerializesOutputSchemasAndAllHints(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != len(canonicalToolManifest) {
		t.Fatalf("tool count=%d want manifest count %d", len(tools), len(canonicalToolManifest))
	}
	previous := ""
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if previous != "" && name < previous {
			t.Fatalf("tools/list is not stable: %s before %s", previous, name)
		}
		previous = name
		if _, ok := tool["outputSchema"].(map[string]any); !ok {
			t.Errorf("%s outputSchema missing", name)
		}
		annotations, ok := tool["annotations"].(map[string]any)
		if !ok {
			t.Errorf("%s annotations missing", name)
			continue
		}
		for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := annotations[key].(bool); !ok {
				t.Errorf("%s annotation %s missing", name, key)
			}
		}
	}
}

func TestOutputContractViolationAndToolErrorsOmitStructuredContent(t *testing.T) {
	tool := Tool{OutputSchema: closedOutput(map[string]any{"value": outputString()}, "value")}
	result := toolResult(tool, map[string]any{"value": 42}, false)
	if result["isError"] != true {
		t.Fatalf("schema mismatch did not fail: %#v", result)
	}
	if _, exists := result["structuredContent"]; exists {
		t.Fatalf("schema mismatch exposed structuredContent: %#v", result)
	}
	result = toolResult(tool, map[string]any{"error": "failed"}, true)
	if result["isError"] != true {
		t.Fatalf("tool failure was not marked: %#v", result)
	}
	if _, exists := result["structuredContent"]; exists {
		t.Fatalf("tool failure exposed structuredContent: %#v", result)
	}
}

func TestTaskReadOutputSchemaAcceptsBothDeclaredShapes(t *testing.T) {
	inactive := map[string]any{
		"task": map[string]any{
			"schema_version": float64(1), "id": "task", "sha256": strings.Repeat("a", 64), "project_id": "project",
			"title": "title", "objective": "objective", "branch": "feature/x", "base_revision": strings.Repeat("b", 40),
			"acceptance_criteria": []any{}, "constraints": []any{}, "status": "created", "created_by": "gpt", "created_at": "2026-07-30T10:00:00Z",
		},
		"state": map[string]any{
			"schema_version": float64(1), "task_id": "task", "task_sha256": strings.Repeat("a", 64), "status": "created", "updated_at": "2026-07-30T10:00:00Z",
		},
		"active_run": false,
	}
	if err := validateOutputValue(toolOutputSchemas["task_read"], inactive); err != nil {
		t.Fatalf("inactive task shape rejected: %v", err)
	}
	active := map[string]any{
		"task": inactive["task"],
		"run": map[string]any{
			"schema_version": float64(1), "id": "run", "task_id": "task", "task_sha256": strings.Repeat("a", 64),
			"project_id": "project", "gateway_id": "home_pc", "branch": "feature/x",
			"base_revision": strings.Repeat("b", 40), "hub_revision": strings.Repeat("c", 40), "status": "awaiting_result",
			"created_at": "2026-07-30T10:00:00Z",
		},
		"project": map[string]any{
			"schema_version": float64(1), "id": "project", "repository_url": "git@example.invalid:project.git", "default_branch": "main",
			"workflow_repository": "rceman/gpt-review-planner", "workflow_commit": strings.Repeat("d", 40), "status": "active",
			"created_at": "2026-07-30T10:00:00Z", "updated_at": "2026-07-30T10:00:00Z",
		},
		"plan": map[string]any{
			"schema_version": float64(model.PlanSchemaVersion), "project_id": "project", "revision": float64(1), "title": "title", "summary": "summary", "current_objective": "objective", "queue": []any{}, "sections": []any{},
			"updated_by": "gpt", "updated_at": "2026-07-30T10:00:00Z",
		},
		"workflow_policy": map[string]any{
			"schema_version": float64(1), "project_id": "project", "revision": float64(1), "workflow_stage": model.WorkflowStageTransitionalMain,
			"integration_branch": "main", "agent": map[string]any{"wait_for_ci": false},
			"ci":         map[string]any{"task": model.WorkflowCIModeDisabled, "task_merge": model.WorkflowCIModeObserve, "release": model.WorkflowCIModeObserve},
			"updated_by": "gpt", "updated_at": "2026-07-30T10:00:00Z",
		},
		"repository_root":  "/tmp/project",
		"finalize_command": "gpt-tunnel run finalize run", "text": "packet",
		"run_summaries": []any{},
	}
	if err := validateOutputValue(toolOutputSchemas["task_read"], active); err != nil {
		if packetErr := validateOutputValue(taskPacketOutputSchema(), active); packetErr != nil {
			t.Fatalf("active task shape rejected: %v (packet: %v)", err, packetErr)
		}
		t.Fatalf("active task shape rejected: %v", err)
	}
}

func TestRunReviewReportSchemasKeepDraftAndFinalParityClosed(t *testing.T) {
	draft := runReviewDraftOutputSchema()
	final := runReviewReportOutputSchema()
	if draft["additionalProperties"] != false || final["additionalProperties"] != false {
		t.Fatal("review report schemas must be closed")
	}
	draftProperties := draft["properties"].(map[string]any)
	finalProperties := final["properties"].(map[string]any)
	for _, name := range []string{"repository_state", "gates", "findings", "scope_coverage", "changed_files"} {
		if _, ok := draftProperties[name]; !ok {
			t.Fatalf("draft schema missing %s", name)
		}
		if _, ok := finalProperties[name]; !ok {
			t.Fatalf("final schema missing %s", name)
		}
	}
	if _, ok := finalProperties["draft_revision"]; ok {
		t.Fatal("final report advertises mutable draft_revision")
	}
	if _, ok := finalProperties["completed_sections"]; ok {
		t.Fatal("final report advertises mutable completed_sections")
	}
	findings := draftProperties["findings"].(map[string]any)
	items := findings["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Fatalf("draft findings are not closed: %#v", findings)
	}
}
