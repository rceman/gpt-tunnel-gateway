package mcp

import "testing"

func TestTaskCorrectionSchemaAcceptsZeroBasedSourcePosition(t *testing.T) {
	schema := taskCorrectionInputSchema()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties=%T", schema["properties"])
	}
	position, ok := properties["source_item_position"].(map[string]any)
	if !ok {
		t.Fatalf("source_item_position=%T", properties["source_item_position"])
	}
	if got, ok := position["minimum"].(int); !ok || got != 0 {
		t.Fatalf("source_item_position minimum=%v", position["minimum"])
	}
}
