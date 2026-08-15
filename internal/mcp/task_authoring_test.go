package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	identifiers, operation, err := server.Service.ProjectIdentifiersAdopt(ctx, service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: revision}})
	if err != nil || identifiers.ProjectCode != "EXM" {
		t.Fatalf("adopt identifiers: %#v %v", identifiers, err)
	}
	configuration, err := server.Service.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Service.Hub.Transact(ctx, operation.Hub.After, "test: seed train_v2 authority", func(worktree string) ([]string, error) {
		path := "gpt-tunnel/v1/projects/example/configuration/current.json"
		latest := configuration
		latest.ExecutionModel = "train_v2"
		latest.Revision = configuration.Revision + 1
		if err := model.ValidateProjectConfiguration(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, latest); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTrainV2TaskAuthoringMCPWiringAndSchemaParity(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	configureTrainV2MCPTest(t, server)
	sessionID := genericSession(t, server.Service, "example")
	for _, path := range []string{"task/create", "task/create_status", "task/update", "task/ready", "task/list", "task/read", "task/supersede_status"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": path}}})))
		if contract["kind"] != "action" || contract["path"] != path {
			t.Fatalf("missing task authoring action contract %s: %#v", path, contract)
		}
		properties := contract["contract"].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
		for _, forbidden := range []string{"branch", "base_revision", "worktree", "agent_id", "session_id", "project_id"} {
			if _, ok := properties[forbidden]; ok {
				t.Fatalf("task authoring schema %s exposes execution field %q", path, forbidden)
			}
		}
	}
	started := time.Now()
	created := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "task/create", "input": map[string]any{"title": "Generic planned task", "objective": "Exercise generic authoring wiring.", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}}}})))
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("public task/create receipt exceeded one second: %s", elapsed)
	} else {
		t.Logf("public task/create receipt: %s", elapsed)
	}
	operationID, ok := created["operation_id"].(string)
	if !ok || operationID == "" || created["status"] != "accepted" {
		t.Fatalf("generic task/create did not return accepted receipt: %#v", created)
	}
	var completed map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		completed = genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "task/create_status", "input": map[string]any{"operation_id": operationID}}}})))
		if completed["status"] == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed["status"] != "completed" || completed["task"].(map[string]any)["status"] != model.TaskAuthoringPlanned {
		t.Fatalf("generic task/create worker did not complete: %#v", completed)
	}
	withProject := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "task/create", "input": map[string]any{"project_id": "example", "title": "Rejected project field", "objective": "The session owns project authority.", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}}}})))
	if withProject["is_error"] != true {
		t.Fatalf("session-bound task/create accepted caller project_id: %#v", withProject)
	}
}

func TestTaskCreateSchemaDescribesLegacyAndTrainV2Branches(t *testing.T) {
	schema := taskAuthoringCreateSchema()
	if schema["additionalProperties"] != false {
		t.Fatalf("task/create root is not closed: %#v", schema)
	}
	branches, ok := schema["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("task/create does not expose exactly two mode branches: %#v", schema["oneOf"])
	}
	legacy := mustJSON(t, map[string]any{
		"project_id": "example", "slug": "legacy-task", "title": "Legacy task",
		"objective": "Use the pre-cutover authoring contract.", "operation_class": "implementation", "created_by": "planner",
	})
	v2 := mustJSON(t, map[string]any{
		"project_id": "example", "title": "Train task", "objective": "Use the Train v2 authoring contract.",
		"adr_relation": model.TaskADRNoRequired, "created_by": "planner",
	})
	if err := validateGenericActionInput(schema, legacy); err != nil {
		t.Fatalf("valid legacy task/create input rejected: %v", err)
	}
	if err := validateGenericActionInput(schema, v2); err != nil {
		t.Fatalf("valid Train v2 task/create input rejected: %v", err)
	}
	for name, raw := range map[string][]byte{
		"legacy missing slug":     mustJSON(t, map[string]any{"project_id": "example", "title": "x", "objective": "y", "operation_class": "implementation", "created_by": "planner"}),
		"v2 missing adr relation": mustJSON(t, map[string]any{"project_id": "example", "title": "x", "objective": "y", "created_by": "planner"}),
		"unknown field":           mustJSON(t, map[string]any{"project_id": "example", "title": "x", "objective": "y", "adr_relation": model.TaskADRNoRequired, "created_by": "planner", "unexpected": true}),
	} {
		if err := validateGenericActionInput(schema, raw); err == nil {
			t.Fatalf("invalid %s input was accepted", name)
		}
	}
}

func createReadyMCPTrainTask(t *testing.T, server *Server) model.TaskAuthoring {
	t.Helper()
	ctx := context.Background()
	revision, err := server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := server.Service.TaskAuthoringCreate(ctx, service.TaskAuthoringCreateInput{ProjectID: "example", Title: "Ready generic Train Task", Objective: "Create a ready Task for generic Train admission.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err := server.Service.TaskAuthoringReady(ctx, service.TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

func TestTrainV2MCPWiringAndSchemaParity(t *testing.T) {
	server := newSessionTestServer(t)
	configureTrainV2MCPTest(t, server)
	first := createReadyMCPTrainTask(t, server)
	sessionID := genericSession(t, server.Service, "example")
	for _, path := range []string{"train/create", "train/add", "train/read", "train/list", "train/start", "train/start_status", "train/advance", "train/advance_status", "train/attempt-finalize", "train/attempt-finalize_status", "train/attempt-review", "train/attempt-review_status", "train/integrate", "train/integrate_status", "train/cutover"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": path}}})))
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
	if created["train"].(map[string]any)["status"] != model.TrainV2Planned {
		t.Fatalf("generic train/create wiring failed: %#v", created)
	}
}
