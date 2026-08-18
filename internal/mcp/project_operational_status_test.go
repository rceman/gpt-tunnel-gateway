package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectStatusIsSessionBoundCompactOperationalRead(t *testing.T) {
	server := newSessionTestServer(t)
	entry, ok := server.genericActionRegistry(server.tools())["project/status"]
	if !ok || !entry.SessionBound || !entry.SessionRequired {
		t.Fatalf("project/status contract=%#v", entry)
	}
	if schemaContainsPropertyForTest(entry.InputSchema, "project_id") {
		t.Fatalf("project/status exposes caller project authority: %#v", entry.InputSchema)
	}
	started := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
	}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "project/status", "input": map[string]any{},
		}},
	})))
	payload, ok := status["result"].(map[string]any)
	if !ok {
		t.Fatalf("project/status result=%#v", status)
	}
	project, ok := payload["project"].(map[string]any)
	if !ok || project["project_id"] != "example" {
		t.Fatalf("project/status was not derived from Session: %#v", status)
	}
	for _, forbidden := range []string{"tasks", "trains", "tail", "history", "project_configuration"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("project/status leaked full field %q: %#v", forbidden, status)
		}
	}
}

func TestProjectStatusPublicCallSurfacesTrainClassificationFailure(t *testing.T) {
	server := newSessionTestServer(t)
	now := time.Now().UTC()
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion, ID: "EXM-TRN1", ProjectID: "example", Revision: 1,
		Status: model.TrainV2Blocked, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
		Items: []model.TrainV2Item{{Position: 0, TaskID: "EXM-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemBlocked, AddedAt: now,
			Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptFailed, AgentID: "agent-1", AirelaySessionKey: "example_master", GatewayID: "test_gateway", StartHead: strings.Repeat("b", 40), StartedAt: now.Add(-time.Minute), FinishedAt: timePtrForMCPTest(now)}}}},
	}
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Service.Hub.Transact(context.Background(), revision, "test: seed project status Train", func(worktree string) ([]string, error) {
		path := hub.ProtocolRoot + "/projects/example/trains-v2/EXM-TRN1.json"
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	operation := map[string]any{
		"schema_version": 1, "operation_id": "mutation-mcp-unknown", "kind": "train-v2-future-mutation",
		"request_sha256": strings.Repeat("a", 64), "project_id": "example", "input": json.RawMessage(`{"train_id":"EXM-TRN1"}`),
		"status": "running", "created_at": now, "updated_at": now,
	}
	operationBytes, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	mutationDir := filepath.Join(server.Service.Config.StateDir, "operations", "mutations")
	if err := os.MkdirAll(mutationDir, 0o700); err != nil {
		t.Fatal(err)
	}
	operationPath := filepath.Join(mutationDir, "mutation-mcp-unknown.json")
	if err := os.WriteFile(operationPath, operationBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(operationPath) })
	started := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
	}))
	sessionID := started["session"].(map[string]any)["session_id"].(string)
	status := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": "project/status", "input": map[string]any{},
		}},
	})))
	payload := status["result"].(map[string]any)
	if payload["state"] != "blocked" || payload["blocker"] != "TRAIN_RECONCILIATION_UNAVAILABLE" {
		t.Fatalf("public project/status omitted Train classification blocker: %#v", payload)
	}
}

func timePtrForMCPTest(value time.Time) *time.Time { return &value }
