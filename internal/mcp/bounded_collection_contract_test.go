package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func isAlwaysAvailableDebugEvidence(path string) bool {
	switch path {
	case "debug/adr_legacy_relations", "debug/task_legacy_revision_list", "debug/task_legacy_revision_read":
		return true
	default:
		return false
	}
}

func TestGrowingCollectionInputsExposeBoundedContinuationContract(t *testing.T) {
	tools := (&Server{Service: service.New(config.Config{MaxListItems: 1000})}).tools()
	for _, name := range []string{"git_refs", "git_log", "git_tree"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		properties := tool.InputSchema["properties"].(map[string]any)
		limit := properties["limit"].(map[string]any)
		if limit["minimum"] != 1 || limit["maximum"] != service.MaxPublicCollectionLimit || limit["default"] != service.DefaultPublicCollectionLimit {
			t.Fatalf("%s limit schema = %#v", name, limit)
		}
		if _, ok := properties["cursor"]; !ok {
			t.Fatalf("%s missing cursor input", name)
		}
		output := tool.OutputSchema
		for _, field := range []string{"next_cursor", "has_more"} {
			if _, ok := output["properties"].(map[string]any)[field]; !ok {
				t.Fatalf("%s missing output field %s", name, field)
			}
		}
	}
}
