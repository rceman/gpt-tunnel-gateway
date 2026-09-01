package mcp

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func configureTrainV2MCPTest(t *testing.T, server *Server) {
	t.Helper()
	ctx := context.Background()
	revision := ensureMCPTestProjectIdentifiers(t, server.Service)
	err := error(nil)
	configuration, err := server.Service.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.Service.Hub.Transact(ctx, revision, "test: seed train_v2 authority", func(worktree string) ([]string, error) {
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

func ensureMCPTestProjectIdentifiers(t *testing.T, s *service.Service) string {
	t.Helper()
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, "example")
	if err == nil {
		if identifiers.ProjectCode != "EXM" {
			t.Fatalf("test project code=%q, want EXM", identifiers.ProjectCode)
		}
		return revision
	}
	if !service.IsNotFound(err) {
		t.Fatal(err)
	}
	identifiers, operation, err := s.ProjectIdentifiersAdopt(ctx, service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: revision}})
	if err != nil || identifiers.ProjectCode != "EXM" {
		t.Fatalf("adopt identifiers: %#v %v", identifiers, err)
	}
	return operation.Hub.After
}

func TestTrainV2TaskAuthoringMCPWiringAndSchemaParity(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithDelivery(context.Background())
	configureTrainV2MCPTest(t, server)
	sessionID := genericSession(t, server.Service, "example")
	for _, path := range []string{"task/create", "task/update", "task/ready", "task/list", "task/read"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": path}}})))
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
	created := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "task/create", "input": map[string]any{"type": "bug", "execution": "hotfix", "scope": map[string]any{"files": []string{"internal/service/task_authoring.go"}, "modules": []string{"gateway"}}, "title": "Generic planned task", "objective": "Exercise generic authoring wiring.", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}}}})))
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("public task/create receipt exceeded one second: %s", elapsed)
	} else {
		t.Logf("public task/create receipt: %s", elapsed)
	}
	operationID, ok := created["operation_id"].(string)
	if !ok || operationID == "" || created["status"] != "accepted" {
		t.Fatalf("generic task/create did not return accepted receipt: %#v", created)
	}
	completed := waitForMCPGenericOperation(t, server, sessionID, operationID)
	if completed["status"] != "completed" {
		t.Fatalf("generic task/create worker did not complete: %#v", completed)
	}
	result, ok := completed["result"].(map[string]any)
	if !ok || result["status"] != "completed" || result["task"] == nil {
		t.Fatalf("generic task/create result=%#v", completed)
	}
	if result["task"].(map[string]any)["type"] != "bug" {
		t.Fatalf("generic task/create lost type: %#v", result["task"])
	}
	createdTask := result["task"].(map[string]any)
	if createdTask["execution"] != "hotfix" || createdTask["scope"].(map[string]any)["files"].([]any)[0] != "internal/service/task_authoring.go" {
		t.Fatalf("generic task/create lost scope or execution: %#v", createdTask)
	}
	withProject := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "task/create", "input": map[string]any{"project_id": "example", "title": "Rejected project field", "objective": "The session owns project authority.", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}}}})))
	if withProject["is_error"] != true {
		t.Fatalf("session-bound task/create accepted caller project_id: %#v", withProject)
	}
}

func TestTaskCreateSchemaUsesTaskTypeAndRejectsLegacyOperationClass(t *testing.T) {
	schema := taskAuthoringCreateSchema()
	if schema["additionalProperties"] != false {
		t.Fatalf("task/create root is not closed: %#v", schema)
	}
	if _, ok := schema["oneOf"]; ok {
		t.Fatalf("task/create still exposes legacy mode branches: %#v", schema)
	}
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["operation_class"]; ok {
		t.Fatal("task/create still exposes operation_class")
	}
	typ := properties["type"].(map[string]any)
	if got := typ["enum"]; !reflect.DeepEqual(got, []string{"task", "bug", "perf", "chore"}) {
		t.Fatalf("task type enum=%#v", got)
	}
	if typ["default"] != "task" {
		t.Fatalf("task type default=%#v", typ["default"])
	}
	for name, schema := range map[string]map[string]any{"create": taskAuthoringCreateSchema(), "update": taskAuthoringUpdateSchema()} {
		properties := schema["properties"].(map[string]any)
		execution := properties["execution"].(map[string]any)
		if !reflect.DeepEqual(execution["enum"], []string{"train", "hotfix"}) {
			t.Fatalf("%s execution schema=%#v", name, execution)
		}
		scope := properties["scope"].(map[string]any)
		if scope["additionalProperties"] != false {
			t.Fatalf("%s scope is not closed: %#v", name, scope)
		}
		scopeProperties := scope["properties"].(map[string]any)
		for _, field := range []string{"files", "modules"} {
			if scopeProperties[field].(map[string]any)["type"] != "array" {
				t.Fatalf("%s scope.%s is not an array: %#v", name, field, scopeProperties[field])
			}
		}
	}
	server := &Server{Service: service.New(config.Config{StateDir: t.TempDir()})}
	listProperties := server.genericActionRegistry(server.tools())["task/list"].InputSchema["properties"].(map[string]any)
	if _, ok := listProperties["execution"]; !ok {
		t.Fatal("task/list does not advertise execution filter")
	}
	valid := mustJSON(t, map[string]any{
		"project_id": "example", "type": "bug", "title": "Bug task", "objective": "Use the canonical Task type.",
		"adr_relation": model.TaskADRNoRequired, "created_by": "planner",
	})
	if err := validateGenericActionInput(schema, valid); err != nil {
		t.Fatalf("valid typed task/create input rejected: %v", err)
	}
	for name, raw := range map[string][]byte{
		"legacy operation class": mustJSON(t, map[string]any{"project_id": "example", "title": "Task", "objective": "objective", "operation_class": "implementation", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}),
		"missing adr relation":   mustJSON(t, map[string]any{"project_id": "example", "title": "Task", "objective": "objective", "created_by": "planner"}),
		"unknown field":          mustJSON(t, map[string]any{"project_id": "example", "title": "x", "objective": "y", "adr_relation": model.TaskADRNoRequired, "created_by": "planner", "unexpected": true}),
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
