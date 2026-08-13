package mcp

import "testing"

func TestTaskExecutionSchemasAreTaskIdentityOnly(t *testing.T) {
	work := taskWorkSchema()
	finalize := taskFinalizeSchema()
	for name, schema := range map[string]map[string]any{"work": work, "finalize": finalize} {
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema has no properties", name)
		}
		if _, ok := properties["completion_file"]; ok {
			t.Fatalf("%s exposes completion_file", name)
		}
		if _, ok := properties["summary"]; ok {
			t.Fatalf("%s exposes summary", name)
		}
	}
	properties := finalize["properties"].(map[string]any)
	if _, ok := properties["task_id"]; !ok {
		t.Fatal("finalize does not expose task_id")
	}
}
