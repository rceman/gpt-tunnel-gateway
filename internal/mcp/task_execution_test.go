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
	}
	finalizeProperties := finalize["properties"].(map[string]any)
	if _, ok := finalizeProperties["task_id"]; !ok {
		t.Fatal("finalize does not expose task_id")
	}
	if _, ok := finalizeProperties["summary"]; !ok {
		t.Fatal("finalize does not expose bounded semantic summary")
	}
	required, ok := finalize["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "task_id" {
		t.Fatalf("finalize required fields=%#v, want task_id only", finalize["required"])
	}
}
