package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func createReadyMCPTrainTask(t *testing.T, server *Server) model.TaskAuthoring {
	t.Helper()
	ctx := context.Background()
	revision, err := server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := server.Service.TaskLifecycleCreate(ctx, service.TaskAuthoringCreateInput{ProjectID: "example", Title: "Ready generic Train Task", Summary: "Ready Train task fixture.", Objective: "Create a ready Task for generic Train admission.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: revision}}, "tsk585-train-fixture")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := server.Service.TaskAuthoringReadyAsync(ctx, service.TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for receipt.Status != "completed" && receipt.Status != "failed" {
		if !time.Now().Before(deadline) {
			t.Fatalf("Task ready operation %s did not reach a terminal state within 10s: %#v", receipt.OperationID, receipt)
		}
		time.Sleep(10 * time.Millisecond)
		receipt, err = server.Service.TaskAuthoringReadyOperationStatus(ctx, receipt.OperationID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if receipt.Status != "completed" || receipt.Task == nil {
		t.Fatalf("Task ready did not complete: %#v", receipt)
	}
	ready := *receipt.Task
	revision, err = server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := "gpt-tunnel/v1/projects/example/tasks-v2/" + ready.ID + ".json"
	if _, err := server.Service.Hub.Transact(ctx, revision, "test: publish ready task", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, ready); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return ready
}

func TestTrainV2MCPWiringAndSchemaParity(t *testing.T) {
	server := newSessionTestServer(t)
	configureTrainV2MCPTest(t, server)
	first := createReadyMCPTrainTask(t, server)
	sessionID := genericSession(t, server.Service, "example")
	for _, path := range []string{"train/create", "train/add", "train/read", "train/list", "train/start", "train/advance", "train/attempt-finalize", "train/attempt-review", "train/integrate", "train/cutover"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": path}}})))
		if contract["kind"] != "action" || contract["path"] != path {
			t.Fatalf("missing Train v2 action contract %s: %#v", path, contract)
		}
		if path == "train/start" || path == "train/integrate" {
			properties := contract["contract"].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
			for _, forbidden := range []string{"worktree_path", "session_key", "base_revision", "lane_branch"} {
				if _, ok := properties[forbidden]; ok {
					t.Fatalf("Train v2 schema exposes host/execution binding %q", forbidden)
				}
			}
		}
	}
	created := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 11, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "train/create", "input": map[string]any{"task_ids": []any{first.ID}, "created_by": "planner"}}}})))
	operationID, ok := created["operation_id"].(string)
	if !ok || operationID == "" || created["status"] != "accepted" {
		t.Fatalf("generic train/create wiring failed: %#v", created)
	}
	completed := waitForMCPGenericOperation(t, server, sessionID, operationID)
	if completed["status"] != "completed" {
		t.Fatalf("generic train/create worker did not complete: %#v", completed)
	}
}

func waitForMCPGenericOperation(t *testing.T, server *Server, sessionID, operationID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": 20, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": "operation/read", "input": map[string]any{"operation_id": operationID},
			}},
		}))
		structured := genericStructured(t, response)
		if structured["is_error"] == true {
			t.Fatalf("operation/read failed: %#v", response)
		}
		payload, ok := structured["result"].(map[string]any)
		if !ok {
			t.Fatalf("operation/read result=%#v", structured)
		}
		if payload["status"] == "completed" || payload["status"] == "failed" || payload["status"] == "outcome_unknown" {
			return payload
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s did not reach a terminal state", operationID)
	return nil
}
