package mcp

import "testing"

func TestTaskExecutionSchemasAreTaskIdentityOnly(t *testing.T) {
	dispatch := taskDispatchSchema()
	status := taskExecutionStatusSchema()
	for name, schema := range map[string]map[string]any{"dispatch": dispatch, "status": status} {
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema has no properties", name)
		}
		if _, ok := properties["completion_file"]; ok {
			t.Fatalf("%s exposes completion_file", name)
		}
	}
	dispatchProperties := dispatch["properties"].(map[string]any)
	if _, ok := dispatchProperties["key"]; !ok {
		t.Fatal("dispatch does not expose key")
	}
	if _, ok := dispatchProperties["agent"]; !ok {
		t.Fatal("dispatch does not expose optional agent")
	}
	for _, legacy := range []string{"agent_key", "agent_id", "session", "task_id"} {
		if _, ok := dispatchProperties[legacy]; ok {
			t.Fatalf("dispatch exposes legacy field %q", legacy)
		}
	}
	statusProperties := status["properties"].(map[string]any)
	if _, ok := statusProperties["key"]; !ok {
		t.Fatal("status does not expose key")
	}
	if required, ok := status["required"].([]string); !ok || len(required) != 1 || required[0] != "key" {
		t.Fatalf("status required fields=%#v, want key only", status["required"])
	}
}
