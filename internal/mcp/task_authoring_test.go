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

func createReadyMCPTrainTask(t *testing.T, server *Server, title string) model.TaskAuthoring {
	t.Helper()
	ctx := context.Background()
	revision, err := server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := server.Service.TaskAuthoringCreate(ctx, service.TaskAuthoringCreateInput{ProjectID: "example", Title: title, Objective: "Create a ready Task for generic Train admission.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err := server.Service.TaskAuthoringReady(ctx, service.TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

func TestTrainV2GenericSchemaAndCalls(t *testing.T) {
	server := newSessionTestServer(t)
	configureTrainV2MCPTest(t, server)
	first := createReadyMCPTrainTask(t, server, "First generic Train Task")
	second := createReadyMCPTrainTask(t, server, "Second generic Train Task")
	sessionID := genericSession(t, server.Service, "example")
	for _, path := range []string{"train/create", "train/add", "train/read", "train/list", "train/start", "train/integrate"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 10, "method": "tools/call",
			"params": map[string]any{"name": "schema", "arguments": map[string]any{"path": path}},
		})))
		if contract["kind"] != "action" || contract["path"] != path {
			t.Fatalf("missing Train v2 action contract %s: %#v", path, contract)
		}
		if path == "train/start" || path == "train/integrate" {
			properties := contract["contract"].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
			for _, forbidden := range []string{"worktree_path", "session_key", "base_revision", "lane_branch"} {
				if _, ok := properties[forbidden]; ok {
					t.Fatalf("train/start exposes host or execution binding %q", forbidden)
				}
			}
		}
	}
	created := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 11, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "train/create", "input": map[string]any{"project_id": "example", "task_ids": []any{first.ID}, "created_by": "planner"},
		}},
	})))
	train := created["train"].(map[string]any)
	if train["id"] != "EXM-TRN1" || train["status"] != model.TrainV2Planned {
		t.Fatalf("unexpected generic Train create: %#v", train)
	}
	added := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 12, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID, "action": "train/add", "input": map[string]any{"project_id": "example", "train_id": "EXM-TRN1", "task_ids": []any{second.ID}, "expected_revision": 1, "added_by": "planner"},
		}},
	})))
	if len(added["train"].(map[string]any)["items"].([]any)) != 2 {
		t.Fatalf("generic Train add did not append item: %#v", added)
	}
	read := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 13, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "train/read", "input": map[string]any{"project_id": "example", "train_id": "EXM-TRN1"}}},
	})))
	if read["id"] != "EXM-TRN1" {
		t.Fatalf("generic Train read shape changed: %#v", read)
	}
	listed := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 14, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "train/list", "input": map[string]any{"project_id": "example"}}},
	})))
	if len(listed["trains"].([]any)) != 1 {
		t.Fatalf("generic Train list shape changed: %#v", listed)
	}
}
