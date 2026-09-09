package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTaskListSchemaUsesCanonicalBoundedSurface(t *testing.T) {
	server := &Server{Service: service.New(config.Config{MaxListItems: 1000})}
	tool := server.genericActionRegistry(server.tools())["task/list"]
	if tool.Name == "" {
		t.Fatal("task/list tool missing")
	}
	if tool.InputSchema["additionalProperties"] != false {
		t.Fatalf("task_list input is not closed: %#v", tool.InputSchema)
	}
	properties := tool.InputSchema["properties"].(map[string]any)
	for _, name := range []string{"cursor", "include_archived"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("task_list input missing %q: %#v", name, properties)
		}
	}
	if _, ok := properties["limit"]; ok {
		t.Fatal("task/list exposes caller-controlled limit")
	}
	output := tool.OutputSchema
	if output["additionalProperties"] != false {
		t.Fatalf("task_list output is not closed: %#v", output)
	}
	outputProperties := output["properties"].(map[string]any)
	for _, name := range []string{"items", "next_cursor"} {
		if _, ok := outputProperties[name]; !ok {
			t.Fatalf("task_list output missing %q: %#v", name, outputProperties)
		}
	}
	if _, ok := outputProperties["has_more"]; ok {
		t.Fatal("task/list exposes legacy has_more")
	}
}
