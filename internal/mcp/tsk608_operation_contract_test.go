package mcp

import "testing"

func TestTSK608OperationActionsAreBoundedReadOnlyAndDistinctFromAgentAwait(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"operation/read", "operation/await"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing %s", path)
		}
		if !entry.LocalReadOnly || !entry.Annotations.ReadOnlyHint || !entry.Annotations.IdempotentHint {
			t.Fatalf("%s is not read-only/idempotent: %#v", path, entry)
		}
		properties, ok := entry.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s input schema has no properties: %#v", path, entry.InputSchema)
		}
		if _, ok := properties["operation_id"]; !ok {
			t.Fatalf("%s does not require operation_id", path)
		}
	}
	if _, ok := entries["agent/await"]; !ok {
		t.Fatal("agent/await is missing")
	}
	if entries["agent/await"].InputSchema["properties"].(map[string]any)["operation_id"] != nil {
		t.Fatal("agent/await exposes operation identity")
	}
	if _, ok := entries["operation/list"]; ok {
		t.Fatal("operation/list is outside the bounded surface")
	}
}
