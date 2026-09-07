package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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
	server.AuthorityContext = authority.WithPlanner(context.Background())
	configureTrainV2MCPTest(t, server)
	sessionID := genericSession(t, server.Service, "example")
	for _, path := range []string{"task/create", "task/update", "task/ready", "task/list", "task/read"} {
		contract := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"session": sessionID, "path": path}}})))
		if contract["kind"] != "action" || contract["path"] != path {
			t.Fatalf("missing task authoring action contract %s: %#v", path, contract)
		}
		properties := contract["contract"].(map[string]any)["input_schema"].(map[string]any)["properties"].(map[string]any)
		forbiddenFields := []string{"branch", "base_revision", "worktree", "agent_id", "session_id", "project_id"}
		if path == "task/create" || path == "task/update" {
			forbiddenFields = append(forbiddenFields, "execution")
		}
		for _, forbidden := range forbiddenFields {
			if _, ok := properties[forbidden]; ok {
				t.Fatalf("task authoring schema %s exposes execution field %q", path, forbidden)
			}
		}
	}
	started := time.Now()
	created := genericActionResult(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "task/create", "input": map[string]any{"type": "bug", "scope": map[string]any{"files": []string{"internal/service/task_authoring.go"}, "modules": []string{"gateway"}}, "title": "Generic planned task", "objective": "Exercise generic authoring wiring.", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}}}})))
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
	if _, ok := createdTask["execution"]; ok || createdTask["scope"].(map[string]any)["files"].([]any)[0] != "internal/service/task_authoring.go" {
		t.Fatalf("generic task/create exposed caller execution or lost scope: %#v", createdTask)
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
		if _, ok := properties["execution"]; ok {
			t.Fatalf("%s exposes server-owned execution", name)
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
	updateSchema := taskAuthoringUpdateSchema()
	updateWithExecution := mustJSON(t, map[string]any{
		"project_id": "example", "task_id": "EXM-TSK1", "expected_revision": 1, "updated_by": "planner", "execution": "train",
	})
	if err := validateGenericActionInput(updateSchema, updateWithExecution); err == nil {
		t.Fatal("task/update accepted caller-controlled execution")
	}
	for name, raw := range map[string][]byte{
		"legacy operation class": mustJSON(t, map[string]any{"project_id": "example", "title": "Task", "objective": "objective", "operation_class": "implementation", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}),
		"caller execution":       mustJSON(t, map[string]any{"project_id": "example", "title": "Task", "objective": "objective", "execution": "hotfix", "adr_relation": model.TaskADRNoRequired, "created_by": "planner"}),
		"missing adr relation":   mustJSON(t, map[string]any{"project_id": "example", "title": "Task", "objective": "objective", "created_by": "planner"}),
		"unknown field":          mustJSON(t, map[string]any{"project_id": "example", "title": "x", "objective": "y", "adr_relation": model.TaskADRNoRequired, "created_by": "planner", "unexpected": true}),
	} {
		if err := validateGenericActionInput(schema, raw); err == nil {
			t.Fatalf("invalid %s input was accepted", name)
		}
	}
}

func TestTaskListLegacyExecutionFilterFailsClosed(t *testing.T) {
	server := newSessionTestServer(t)
	sessionID := genericSession(t, server.Service, "example")
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": sessionID,
			"action":     "task/list",
			"input":      map[string]any{"execution": "train"},
		}},
	}))
	structured := genericStructured(t, response)
	if structured["is_error"] != true {
		t.Fatalf("legacy task/list silently ignored execution filter: %#v", structured)
	}
	errorResult, ok := structured["result"].(map[string]any)
	errorJSON, _ := json.Marshal(errorResult["error"])
	if !ok || !strings.Contains(string(errorJSON), "execution filter is unavailable") {
		t.Fatalf("legacy task/list returned unexpected error: %#v", structured)
	}
}
