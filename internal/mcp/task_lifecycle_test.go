package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTaskLifecycleToolsHaveStrictContracts(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tools := server.tools()
	for _, name := range []string{"task_mark_merge_ready", "task_defer", "task_mark_merged"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("%s input schema is not closed: %#v", name, tool.InputSchema)
		}
		if tool.OutputSchema["additionalProperties"] != false {
			t.Fatalf("%s output schema is not closed: %#v", name, tool.OutputSchema)
		}
		if tool.Annotations.ReadOnlyHint || !tool.Annotations.DestructiveHint {
			// Lifecycle transitions are durable mutations, not read-only calls.
			t.Fatalf("%s lacks explicit durable-mutation annotations: %#v", name, tool.Annotations)
		}
	}
}
