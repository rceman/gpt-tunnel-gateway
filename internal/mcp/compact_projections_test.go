package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

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

func TestSchemaDomainDiscoveryIsCompactFromContracts(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}),
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	entries := server.genericActionRegistry(server.tools())
	compact, err := genericSchemaV2(entries, "task")
	if err != nil {
		t.Fatal(err)
	}
	complete, err := genericSchemaV2(entries, "task/create")
	if err != nil {
		t.Fatal(err)
	}
	compactActions := compact["actions"].([]map[string]any)
	if len(compactActions) == 0 {
		t.Fatal("compact task domain omitted actions")
	}
	if _, ok := compactActions[0]["input_schema"]; ok {
		t.Fatalf("compact domain leaked input schema: %#v", compactActions[0])
	}
	contract := complete["contract"].(map[string]any)
	if _, ok := contract["input_schema"]; !ok {
		t.Fatalf("exact action omitted input schema: %#v", contract)
	}
}

func TestCanonicalTaskReadPreservesFullPayload(t *testing.T) {
	value := map[string]any{
		"key": "GTW-TSK1", "revision": 1, "title": "Task", "summary": "A compact summary.",
		"status": "planned", "objective": "The complete canonical objective.",
		"acceptance_criteria": []any{"large detail"}, "created_at": "2026-01-01T00:00:00Z",
	}
	result := compactActionResult("task/read", value)
	if _, ok := result["objective"]; !ok {
		t.Fatalf("canonical task read lost objective: %#v", result)
	}
	if result["key"] != "GTW-TSK1" || result["revision"] != 1 || result["status"] != "planned" {
		t.Fatalf("canonical task read lost identity/status: %#v", result)
	}
	if _, ok := result["detail"]; ok {
		t.Fatalf("canonical task read exposed detail control: %#v", result)
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
	compact := compactActionResult("agent/prompt", value)
	result, ok := compact["result"].(map[string]any)
	if !ok || len(result) != 1 || result["project_id"] != "example" {
		t.Fatalf("compact successful Agent result was not project-only: %#v", compact)
	}
}

func TestCompactMutationDoesNotLeakNestedDurablePayloads(t *testing.T) {
	value := map[string]any{
		"agent":         map[string]any{"agent_id": "coder", "secret": "agent-detail"},
		"guide":         map[string]any{"project_id": "example", "revision": float64(2), "content": "full guide"},
		"configuration": map[string]any{"project_id": "example", "revision": float64(2), "gate_commands": "full commands"},
		"policy":        map[string]any{"project_id": "example", "revision": float64(2), "gates": []any{"format"}, "secret": "policy-detail"},
		"identifiers":   map[string]any{"project_id": "example", "project_code": "EXM", "next_task_number": float64(2), "secret": "counter-detail"},
		"adr":           map[string]any{"id": "GTW-ADR1", "title": "ADR", "context": "full context"},
	}
	compact := compactActionResult("journal/add", value)
	for key, forbidden := range map[string]string{
		"agent": "secret", "guide": "content", "configuration": "gate_commands", "policy": "secret", "identifiers": "secret", "adr": "context",
	} {
		object, ok := compact[key].(map[string]any)
		if !ok {
			t.Fatalf("compact mutation lost %s object: %#v", key, compact)
		}
		if _, leaked := object[forbidden]; leaked {
			t.Fatalf("compact mutation leaked %s.%s: %#v", key, forbidden, compact)
		}
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
