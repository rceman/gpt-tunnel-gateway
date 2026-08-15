package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTaskTrainActionsAdvertiseOptionalDetailProjection(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}), AuthorityContext: authority.WithDelivery(context.Background())}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"task/list", "task/read", "task/create", "task/create_status", "train/list", "train/read", "train/create", "train/start_status", "project/update", "agent/prompt", "watcher/nudge"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing action %s", path)
		}
		properties, ok := entry.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("action %s has no closed properties", path)
		}
		if _, ok := properties["detail"]; !ok {
			t.Fatalf("action %s does not advertise detail", path)
		}
	}
}

func TestSchemaDomainDiscoveryIsCompactUnlessDetailRequested(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}), AuthorityContext: authority.WithDelivery(context.Background())}
	entries := server.genericActionRegistry(server.tools())
	compact, err := server.genericSchema(server.tools(), json.RawMessage(`{"path":"task"}`))
	if err != nil {
		t.Fatal(err)
	}
	complete, err := server.genericSchema(server.tools(), json.RawMessage(`{"path":"task","detail":true}`))
	if err != nil {
		t.Fatal(err)
	}
	compactActions := compact.(map[string]any)["actions"].([]map[string]any)
	_ = entries
	if len(compactActions) == 0 {
		t.Fatal("compact task domain omitted actions")
	}
	if _, ok := compactActions[0]["input_schema"]; ok {
		t.Fatalf("compact domain leaked input schema: %#v", compactActions[0])
	}
	completeActions := complete.(map[string]any)["actions"].([]map[string]any)
	if _, ok := completeActions[0]["input_schema"]; !ok {
		t.Fatalf("detailed domain omitted input schema: %#v", completeActions[0])
	}
}

func TestCompactTaskAndTrainResultsDropLargePayloads(t *testing.T) {
	task := map[string]any{
		"id": "GTW-TSK1", "title": "Task", "status": "ready", "objective": "secret detail",
		"acceptance_criteria": []any{"large detail"}, "created_at": "2026-01-01T00:00:00Z",
	}
	compactTaskResult := compactActionResult("task/read", map[string]any{"task": task}, false)
	compactTask := compactTaskResult["task"].(map[string]any)
	if _, ok := compactTask["objective"]; ok {
		t.Fatalf("compact task retained objective: %#v", compactTask)
	}
	if compactTask["id"] != "GTW-TSK1" || compactTask["status"] != "ready" {
		t.Fatalf("compact task lost identity/status: %#v", compactTask)
	}
	full := compactActionResult("task/read", map[string]any{"task": task}, true)
	if _, ok := full["task"].(map[string]any)["objective"]; !ok {
		t.Fatal("detail=true did not preserve full task")
	}

	train := map[string]any{"id": "GTW-TRN1", "status": "running", "items": []any{map[string]any{}, map[string]any{}}, "full_proof": map[string]any{"gate_results": []any{"large"}}}
	compactTrainResult := compactActionResult("train/read", train, false)
	if compactTrainResult["item_count"] != 2 {
		t.Fatalf("compact train omitted item count: %#v", compactTrainResult)
	}
	if _, ok := compactTrainResult["items"]; ok {
		t.Fatalf("compact train retained items: %#v", compactTrainResult)
	}
}

func TestGenericDispatchUsesCompactDefaultAndHonorsDetail(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}), AuthorityContext: authority.WithDelivery(context.Background())}
	if err := server.RegisterGenericAction(GenericAction{
		Path:        "task/probe",
		Description: "Projection test action.",
		InputSchema: obj(map[string]any{"value": str("Value.")}, "value"),
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		},
		Execute: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"task": map[string]any{"id": "GTW-TSK1", "status": "ready", "objective": "detail"}}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	entries := server.genericActionRegistry(server.tools())
	sessionID := genericSession(t, server.Service, "example")
	record, err := durableSession.NewStore(server.Service.Config.StateDir).Get(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := server.genericDispatch(authority.WithDelivery(context.Background()), entries, record, "task/probe", json.RawMessage(`{"value":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := compact["result"].(map[string]any)["task"].(map[string]any)["objective"]; ok {
		t.Fatalf("compact dispatch leaked detail: %#v", compact)
	}
	full, err := server.genericDispatch(authority.WithDelivery(context.Background()), entries, record, "task/probe", json.RawMessage(`{"value":"x","detail":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if full["result"].(map[string]any)["task"].(map[string]any)["objective"] != "detail" {
		t.Fatalf("detail dispatch did not preserve detail: %#v", full)
	}
}
