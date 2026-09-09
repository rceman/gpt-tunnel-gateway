package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTrainActionsAdvertiseOptionalDetailProjection(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"train/list", "train/read"} {
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

func TestControlAndReceiptActionsDoNotAdvertiseDetailProjection(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"operation/read", "agent/prompt", "runtime/restart"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing action %s", path)
		}
		properties, ok := entry.InputSchema["properties"].(map[string]any)
		if ok {
			if _, hasDetail := properties["detail"]; hasDetail {
				t.Fatalf("control or receipt action %s advertises detail", path)
			}
		}
	}
}

func TestSchemaDomainDiscoveryIsCompactUnlessDetailRequested(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
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

func TestCanonicalTaskReadPreservesFullPayload(t *testing.T) {
	task := map[string]any{
		"id": "GTW-TSK1", "title": "Task", "status": "ready", "objective": "secret detail",
		"acceptance_criteria": []any{"large detail"}, "created_at": "2026-01-01T00:00:00Z",
	}
	compactTaskResult := compactActionResult("task/read", map[string]any{"task": task}, false)
	compactTask := compactTaskResult["task"].(map[string]any)
	if _, ok := compactTask["objective"]; !ok {
		t.Fatalf("canonical task read lost objective: %#v", compactTask)
	}
	if compactTask["id"] != "GTW-TSK1" || compactTask["status"] != "ready" {
		t.Fatalf("compact task lost identity/status: %#v", compactTask)
	}
}

func TestCompactSuccessfulAgentPromptKeepsOnlyProjectID(t *testing.T) {
	value := map[string]any{
		"operation_id": "OP-AGENT", "status": "completed",
		"result": map[string]any{
			"project_id": "example", "delivered": true, "outcome": "acknowledged",
			"stdout": "large execution output", "stderr": "large diagnostic output",
		},
	}
	compact := compactActionResult("agent/prompt", value, false)
	result, ok := compact["result"].(map[string]any)
	if !ok || len(result) != 1 || result["project_id"] != "example" {
		t.Fatalf("compact successful Agent result was not project-only: %#v", compact)
	}
}

func TestEveryRegisteredActionHasExplicitProjectionClassification(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		class, ok := projectionClasses[path]
		if !ok {
			t.Errorf("active action %q has no explicit projection classification", path)
			continue
		}
		if class < projectionCompactDefault || class > projectionIntentionalPayload {
			t.Errorf("active action %q has invalid projection classification %d", path, class)
		}
		if compactProjectionAction(path) != (class == projectionCompactDefault) {
			t.Errorf("active action %q classification disagrees with compact projection dispatch", path)
		}
	}
	for path := range projectionClasses {
		if _, ok := entries[path]; !ok {
			t.Errorf("projection classification is not attached to active action %q", path)
		}
	}
}
