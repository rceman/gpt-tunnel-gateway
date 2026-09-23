package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestPublicGitRevisionInputsRejectFullObjectIDs(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tools := server.tools()
	for _, actionField := range []struct {
		name  string
		field string
	}{
		{name: "git_log", field: "revision"},
		{name: "git_show", field: "revision"},
		{name: "git_tree", field: "revision"},
		{name: "git_read_file", field: "revision"},
		{name: "git_diff", field: "from_revision"},
		{name: "git_diff", field: "to_revision"},
		{name: "git_compare", field: "left"},
		{name: "git_compare", field: "right"},
		{name: "git_merge_base", field: "left"},
		{name: "git_merge_base", field: "right"},
	} {
		tool, exists := tools[actionField.name]
		if !exists {
			t.Fatalf("missing Git tool %q", actionField.name)
		}
		properties, ok := tool.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s input schema has no properties", actionField.name)
		}
		schema, ok := properties[actionField.field].(map[string]any)
		if !ok {
			t.Fatalf("%s input schema has no %q field: %#v", actionField.name, actionField.field, properties)
		}
		if err := validateSchemaValue(schema, "abcdef01", actionField.field); err != nil {
			t.Fatalf("%s.%s rejected compact fingerprint: %v", actionField.name, actionField.field, err)
		}
		for _, value := range []string{strings.Repeat("a", 40), strings.Repeat("a", 64), "abcdef012", "ABCDEF01"} {
			if err := validateSchemaValue(schema, value, actionField.field); err == nil {
				t.Fatalf("%s.%s accepted a noncompact Git fingerprint %q", actionField.name, actionField.field, value)
			}
		}
		if err := validateSchemaValue(schema, "refs/heads/ABCDEF01", actionField.field); err != nil {
			t.Fatalf("%s.%s rejected an explicitly qualified ref: %v", actionField.name, actionField.field, err)
		}
	}
}

func TestOutputSchemaValidatesNumbersPreservedByProjection(t *testing.T) {
	schema := closedOutput(map[string]any{"count": integer("Count", 0, 10)}, "count")
	if err := validateOutputValue(schema, map[string]any{"count": json.Number("7")}); err != nil {
		t.Fatalf("projected integer rejected: %v", err)
	}
	for _, value := range []json.Number{"7.5", "11"} {
		if err := validateOutputValue(schema, map[string]any{"count": value}); err == nil {
			t.Fatalf("invalid projected integer accepted: %s", value)
		}
	}
}

func TestPublicGitFingerprintsRequireEightLowercaseHexCharacters(t *testing.T) {
	for _, schema := range []map[string]any{publicGitFingerprintOutputSchema(), publicGitFingerprintOrEmptyOutputSchema()} {
		if err := validateSchemaValue(schema, "abcdef01", "fingerprint"); err != nil {
			t.Fatalf("valid public fingerprint rejected: %v", err)
		}
		for _, value := range []string{"ABCDEF01", "abcdef0", "abcdef012", strings.Repeat("a", 40)} {
			if err := validateSchemaValue(schema, value, "fingerprint"); err == nil {
				t.Fatalf("invalid public fingerprint accepted: %q", value)
			}
		}
	}
}

func TestCollectionOutputSchemasKeepContinuationInTheEnvelope(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tools := server.tools()
	for _, name := range []string{"git_refs", "git_log", "git_tree"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("missing collection tool %q", name)
		}
		properties, _ := tool.OutputSchema["properties"].(map[string]any)
		result, _ := properties["result"].(map[string]any)
		resultProperties, _ := result["properties"].(map[string]any)
		for _, field := range []string{"cursor", "next_cursor", "has_more", "_pagination"} {
			if _, exists := resultProperties[field]; exists {
				t.Fatalf("%s result schema exposes continuation field %q", name, field)
			}
		}
		pagination, _ := properties["pagination"].(map[string]any)
		paginationProperties, _ := pagination["properties"].(map[string]any)
		if _, ok := paginationProperties["next_cursor"]; !ok {
			t.Fatalf("%s outer pagination schema has no next_cursor", name)
		}
	}
	entries := server.genericActionRegistry(tools)
	for _, path := range []string{"runtime/logs", "message/list", "code/worktree", "code/tree", "code/read", "code/search", "code/diff", "task/list", "track/list"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing collection action %q", path)
		}
		properties, _ := entry.OutputSchema["properties"].(map[string]any)
		for _, field := range []string{"cursor", "next_cursor", "has_more", "_pagination"} {
			if _, exists := properties[field]; exists {
				t.Fatalf("%s result schema exposes continuation field %q", path, field)
			}
		}
	}
}

func TestGrowingCollectionInputsExposeBoundedContinuationContract(t *testing.T) {
	server := &Server{Service: service.New(config.Config{MaxListItems: 1000})}
	tools := server.tools()
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
		cursor, ok := properties["cursor"].(map[string]any)
		if !ok || cursor["minLength"] != 8 || cursor["maxLength"] != 8 || cursor["pattern"] != `^[ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789]{8}$` {
			t.Fatalf("%s cursor input is not a compact server handle: %#v", name, cursor)
		}
	}
}
