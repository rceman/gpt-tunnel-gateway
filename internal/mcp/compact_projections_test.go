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

func TestTaskTrainActionsAdvertiseOptionalDetailProjection(t *testing.T) {
	server := &Server{
		Service:          service.New(config.Config{GatewayID: "compact-test", StateDir: t.TempDir()}),
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"task/list", "task/read", "train/list", "train/read"} {
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
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"task/create_status", "train/start_status", "agent/prompt", "project/update", "watcher/nudge", "runtime/restart"} {
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
		AuthorityContext: authority.WithDelivery(context.Background()),
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

func TestCompactTaskRevisionListDropsRevisionPayloads(t *testing.T) {
	compact := compactActionResult("task/revision_list", map[string]any{
		"revisions": []any{map[string]any{
			"id": "GTW-TSK1", "revision": float64(2), "sha256": "sha",
			"status": "ready", "title": "Task", "objective": "large detail",
		}},
		"next_cursor": "cursor", "has_more": false,
	}, false)
	revision := compact["revisions"].([]any)[0].(map[string]any)
	if _, ok := revision["objective"]; ok {
		t.Fatalf("compact revision retained objective: %#v", revision)
	}
	if revision["id"] != "GTW-TSK1" || revision["revision"] != float64(2) {
		t.Fatalf("compact revision lost identity: %#v", revision)
	}
}

func TestCompactTrainMutationDropsItemPayload(t *testing.T) {
	compact := compactActionResult("train/create", map[string]any{
		"operation_id": "OP-1", "status": "completed",
		"train": map[string]any{
			"id": "GTW-TRN1", "project_id": "example", "revision": float64(2),
			"status": "planned", "items": []any{map[string]any{"task_id": "GTW-TSK1", "attempts": []any{"large"}}},
		},
	}, false)
	train := compact["train"].(map[string]any)
	if _, ok := train["items"]; ok {
		t.Fatalf("compact train retained items: %#v", train)
	}
	if train["id"] != "GTW-TRN1" || train["item_count"] != 1 {
		t.Fatalf("compact train lost identity/count: %#v", train)
	}
}

func TestCompactMutationPreservesStrictTaskReceiptSchema(t *testing.T) {
	receipt := map[string]any{
		"operation_id": "OP-1", "status": "completed", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:01Z",
		"task": map[string]any{
			"schema_version": float64(1), "id": "GTW-TSK1", "sha256": "task-sha", "project_id": "example",
			"title": "Task", "objective": "Objective", "branch": "task/example",
			"acceptance_criteria": []any{"AC1"}, "constraints": []any{"bounded"}, "status": "ready",
			"created_by": "planner", "created_at": "2026-01-01T00:00:00Z", "metadata": map[string]any{"large": true},
		},
	}
	compact := compactActionResult("task/supersede", receipt, false)
	if err := validateOutputValue(taskSupersedeReceiptOutputSchema(), compact); err != nil {
		t.Fatalf("compact receipt violates strict output schema: %v; result=%#v", err, compact)
	}
	if _, ok := compact["task"].(map[string]any)["metadata"]; ok {
		t.Fatalf("compact receipt retained optional task metadata: %#v", compact)
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

func TestCompactMutationDoesNotLeakNestedDurablePayloads(t *testing.T) {
	value := map[string]any{
		"agent":         map[string]any{"agent_id": "coder", "secret": "agent-detail"},
		"guide":         map[string]any{"project_id": "example", "revision": float64(2), "content": "full guide"},
		"configuration": map[string]any{"project_id": "example", "revision": float64(2), "gate_commands": "full commands"},
		"policy":        map[string]any{"project_id": "example", "revision": float64(2), "gates": []any{"format"}, "secret": "policy-detail"},
		"identifiers":   map[string]any{"project_id": "example", "project_code": "EXM", "next_task_number": float64(2), "secret": "counter-detail"},
		"adr":           map[string]any{"id": "GTW-ADR1", "title": "ADR", "context": "full context"},
	}
	compact := compactActionResult("project/workflow_policy_update", value, false)
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
