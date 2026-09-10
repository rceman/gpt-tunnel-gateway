package mcp

import "testing"

func TestTSK521TaskExecutionSchemasAreClosedAndPubliclyBounded(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	dispatch, ok := entries["task/dispatch"]
	if !ok {
		t.Fatal("task/dispatch is not registered")
	}
	status, ok := entries["task/status"]
	if !ok {
		t.Fatal("task/status is not registered")
	}
	assertKeys := func(name string, schema map[string]any, want []string) {
		t.Helper()
		properties := schema["properties"].(map[string]any)
		if len(properties) != len(want) {
			t.Fatalf("%s properties=%v", name, properties)
		}
		for _, key := range want {
			if _, ok := properties[key]; !ok {
				t.Fatalf("%s missing %q", name, key)
			}
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s is not closed", name)
		}
	}
	assertKeys("dispatch input", dispatch.InputSchema, []string{"key", "agent"})
	assertKeys("status input", status.InputSchema, []string{"key"})
	assertKeys("dispatch output", dispatch.OutputSchema, []string{"key", "status", "stage", "worktree", "head", "agent", "execution_revision", "updated_at"})
	assertKeys("status output", status.OutputSchema, []string{"key", "status", "stage", "worktree", "head", "agent", "execution_revision", "updated_at"})
	for _, schema := range []map[string]any{dispatch.OutputSchema, status.OutputSchema} {
		properties := schema["properties"].(map[string]any)
		for _, forbidden := range []string{"agent_key", "agent_id", "session"} {
			if _, ok := properties[forbidden]; ok {
				t.Fatalf("public execution schema exposes %s", forbidden)
			}
		}
	}
	for name, schema := range map[string]map[string]any{"dispatch": dispatch.OutputSchema, "status": status.OutputSchema} {
		properties := schema["properties"].(map[string]any)
		head := properties["head"].(map[string]any)
		if head["pattern"] != "^[a-f0-9]{8}$" {
			t.Fatalf("%s head schema=%v", name, head)
		}
	}
	if got := status.OutputSchema["required"].([]string); len(got) != 2 || got[0] != "key" || got[1] != "status" {
		t.Fatalf("status required=%v", got)
	}
}
