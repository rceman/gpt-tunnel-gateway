package mcp

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func configureTrainV2MCPTest(t *testing.T, server *Server) {
	t.Helper()
	ctx := context.Background()
	revision, err := server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identifiers, operation, err := server.Service.ProjectIdentifiersAdopt(ctx, service.ProjectIdentifiersAdoptInput{
		ProjectID: "example", ProjectCode: "EXM",
		WriteOptions: service.WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil || identifiers.ProjectCode != "EXM" {
		t.Fatalf("adopt identifiers: %#v %v", identifiers, err)
	}
	configuration, err := server.Service.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	executionModel := "train_v2"
	if _, _, err := server.Service.ProjectConfigurationUpdate(service.WithPlannerWorkflowPolicyAuthority(ctx), service.ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: configuration.Revision,
		Patch:            service.ProjectConfigurationPatch{ExecutionModel: &executionModel},
		UpdatedBy:        "planner",
		WriteOptions:     service.WriteOptions{ExpectedHubRevision: operation.Hub.After},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTrainV2TaskAuthoringGenericSchemaAndCalls(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	configureTrainV2MCPTest(t, server)
	sessionID := genericSession(t, server.Service, "example")

	for _, path := range []string{"task/create", "task/update", "task/ready", "task/list", "task/read"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": path}},
		})))
		if contract["kind"] != "action" || contract["path"] != path {
			t.Fatalf("missing train_v2 action contract %s: %#v", path, contract)
		}
		properties := contract["contract"].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
		for _, forbidden := range []string{"branch", "base_revision", "worktree", "agent_id", "session_id"} {
			if _, ok := properties[forbidden]; ok {
				t.Fatalf("task authoring schema %s exposes execution field %q", path, forbidden)
			}
		}
	}

	created := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/create", "input": map[string]any{
				"project_id": "example", "title": "Generic planned task", "objective": "Exercise the generic authoring action.",
				"acceptance_criteria": []any{"durable"}, "adr_relation": model.TaskADRNoRequired, "created_by": "planner",
			},
		}},
	})))
	task := created["task"].(map[string]any)
	if task["id"] != "EXM-TSK1" || task["status"] != model.TaskAuthoringPlanned {
		t.Fatalf("unexpected generic created task: %#v", task)
	}

	updated := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/update", "input": map[string]any{
				"project_id": "example", "task_id": "EXM-TSK1", "expected_revision": 1,
				"expected_revision_sha256": task["revision_sha256"], "title": "Updated generic planned task", "updated_by": "planner",
			},
		}},
	})))
	updatedTask := updated["task"].(map[string]any)
	if updatedTask["revision"] != float64(2) || updatedTask["title"] != "Updated generic planned task" {
		t.Fatalf("unexpected generic update: %#v", updatedTask)
	}

	readied := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/ready", "input": map[string]any{
				"project_id": "example", "task_id": "EXM-TSK1", "expected_revision": 2,
				"expected_revision_sha256": updatedTask["revision_sha256"], "ready_by": "planner",
			},
		}},
	})))
	if readied["task"].(map[string]any)["status"] != model.TaskAuthoringReady {
		t.Fatalf("task was not readied: %#v", readied)
	}

	listed := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/list", "input": map[string]any{"project_id": "example", "status": model.TaskAuthoringReady, "limit": 10},
		}},
	})))
	if len(listed["tasks"].([]any)) != 1 {
		t.Fatalf("unexpected generic task list: %#v", listed)
	}
	read := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "task/read", "input": map[string]any{"task_id": "EXM-TSK1"},
		}},
	})))
	if read["task"].(map[string]any)["id"] != "EXM-TSK1" {
		t.Fatalf("unexpected generic task read: %#v", read)
	}
}
