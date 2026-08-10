package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTaskListSchemaExposesBoundedSearchStatusAndCursor(t *testing.T) {
	tool := (&Server{Service: service.New(config.Config{MaxListItems: 1000})}).tools()["task_list"]
	if tool.Name == "" {
		t.Fatal("task_list tool missing")
	}
	if tool.InputSchema["additionalProperties"] != false {
		t.Fatalf("task_list input is not closed: %#v", tool.InputSchema)
	}
	properties := tool.InputSchema["properties"].(map[string]any)
	for _, name := range []string{"project_id", "query", "status", "limit", "cursor"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("task_list input missing %q: %#v", name, properties)
		}
	}
	if properties["query"].(map[string]any)["maxLength"] != 256 {
		t.Fatalf("query is not bounded: %#v", properties["query"])
	}
	if properties["limit"].(map[string]any)["maximum"] != service.MaxTaskListLimit {
		t.Fatalf("limit maximum=%v", properties["limit"])
	}
	if properties["limit"].(map[string]any)["default"] != service.DefaultTaskListLimit {
		t.Fatalf("limit default=%v", properties["limit"])
	}
	status := properties["status"].(map[string]any)["enum"].([]any)
	if len(status) != 9 {
		t.Fatalf("status enum=%#v", status)
	}
	output := tool.OutputSchema
	if output["additionalProperties"] != false {
		t.Fatalf("task_list output is not closed: %#v", output)
	}
	outputProperties := output["properties"].(map[string]any)
	for _, name := range []string{"tasks", "next_cursor", "has_more"} {
		if _, ok := outputProperties[name]; !ok {
			t.Fatalf("task_list output missing %q: %#v", name, outputProperties)
		}
	}
}
