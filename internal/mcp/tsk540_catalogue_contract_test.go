package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK540CatalogueSchemasUseOuterContinuationAndCompactMilestones(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	milestoneItem := schemaProperties(entries["milestone/list"].OutputSchema)["items"].(map[string]any)["items"].(map[string]any)
	milestoneProperties := schemaProperties(milestoneItem)
	for _, field := range []string{"key", "revision", "title", "summary", "status", "updated_at"} {
		if _, ok := milestoneProperties[field]; !ok {
			t.Fatalf("milestone catalogue item missing %q: %#v", field, milestoneProperties)
		}
	}
	for _, field := range []string{"tasks", "report", "task_count"} {
		if _, ok := milestoneProperties[field]; ok {
			t.Fatalf("milestone catalogue item exposes forbidden %q: %#v", field, milestoneProperties)
		}
	}
	if len(milestoneProperties) != 6 {
		t.Fatalf("milestone catalogue item has unexpected fields: %#v", milestoneProperties)
	}
	required := stringList(milestoneItem["required"])
	if len(required) != 4 || required[0] != "key" || required[1] != "revision" || required[2] != "title" || required[3] != "status" {
		t.Fatalf("milestone catalogue required=%v", required)
	}
	for _, path := range []string{"milestone/list", "milestone/query"} {
		if properties := schemaProperties(entries[path].OutputSchema); len(properties) != 1 || properties["items"] == nil {
			t.Fatalf("%s output is not exactly items-only: %#v", path, properties)
		}
	}
	readProperties := schemaProperties(entries["milestone/read"].OutputSchema)
	for _, field := range []string{"tasks", "report"} {
		if _, ok := readProperties[field]; !ok {
			t.Fatalf("milestone/read lost full detail field %q: %#v", field, readProperties)
		}
	}
	for _, path := range []string{
		"adr/list", "adr/query", "adr/history",
		"rule/list", "rule/query", "rule/history",
		"task/list", "task/query", "task/history",
		"track/list", "track/query", "track/history",
		"milestone/list", "milestone/query", "milestone/history",
		"journal/list", "relation/list", "debug/task_legacy_revision_list",
	} {
		for _, field := range []string{"next_cursor", "_pagination"} {
			if _, ok := schemaProperties(entries[path].OutputSchema)[field]; ok {
				t.Fatalf("%s output exposes result-level %s", path, field)
			}
		}
	}
	if _, ok := schemaProperties(entries["message/list"].OutputSchema)["cursor"]; !ok {
		t.Fatal("message/list lost its approved domain cursor")
	}
}

func TestTSK540MilestoneListUsesOuterPaginationAcrossDeterministicPages(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	_ = server.tools()
	sessionID := genericSession(t, server.Service, "example")
	call := func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		value, err := server.genericCallPublic(server.AuthorityContext, nil, mustJSON(t, map[string]any{
			"session": sessionID,
			"action":  action,
			"input":   input,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return value.(map[string]any)
	}
	const total = 257
	for index := 0; index < total; index++ {
		if _, _, err := server.Service.MilestoneLifecycleCreate(context.Background(), service.MilestoneCreateInput{
			ProjectID: "example", Title: fmt.Sprintf("Milestone %03d", index), CreatedBy: "planner",
		}, ""); err != nil {
			t.Fatalf("milestone seed %d failed: %v", index, err)
		}
	}
	cursor := ""
	pages := 0
	seen := 0
	seenKeys := map[string]bool{}
	for {
		input := map[string]any{}
		if cursor != "" {
			input["cursor"] = cursor
		}
		envelope := call(total+pages+1, "milestone/list", input)
		if envelope["ok"] != true {
			t.Fatalf("milestone/list page %d failed: %#v", pages, envelope)
		}
		result := envelope["result"].(map[string]any)
		for _, field := range []string{"next_cursor", "_pagination", "tasks", "report", "task_count"} {
			if _, ok := result[field]; ok {
				t.Fatalf("milestone/list result exposed %q: %#v", field, result)
			}
		}
		items := result["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("milestone/list page %d was empty", pages)
		}
		for _, raw := range items {
			item := raw.(map[string]any)
			if len(item) < 4 || len(item) > 6 {
				t.Fatalf("milestone/list item has unexpected fields: %#v", item)
			}
			for field := range item {
				switch field {
				case "key", "revision", "title", "summary", "status", "updated_at":
				default:
					t.Fatalf("milestone/list item exposed unexpected %q: %#v", field, item)
				}
			}
			key := item["key"].(string)
			if seenKeys[key] {
				t.Fatalf("milestone/list repeated %s across pages", key)
			}
			seenKeys[key] = true
		}
		seen += len(items)
		pages++
		paginationValue, hasMore := envelope["pagination"].(map[string]any)
		if !hasMore {
			break
		}
		cursor, _ = paginationValue["next_cursor"].(string)
		if cursor == "" {
			t.Fatalf("milestone/list page %d returned empty outer cursor", pages)
		}
		if pages > total {
			t.Fatalf("milestone/list did not terminate after %d pages", pages)
		}
	}
	if pages < 2 || seen != total {
		t.Fatalf("milestone/list pagination pages=%d items=%d want pages>=2 items=%d", pages, seen, total)
	}
}
