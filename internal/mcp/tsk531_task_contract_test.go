package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestTSK531CanonicalTaskSurfaceAndLegacyEvidenceContract(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())

	wantCanonical := map[string]bool{
		"task/create": true, "task/read": true, "task/update": true,
		"task/list": true, "task/query": true, "task/archive": true, "task/history": true,
		"task/dispatch": true, "task/status": true, "task/test": true, "task/integrate": true,
		"task/complete": true, "task/review": true, "task/review_decide": true, "task/rework": true, "task/guide": true,
	}
	for path := range wantCanonical {
		if _, ok := entries[path]; !ok {
			t.Fatalf("missing canonical Task action %q", path)
		}
	}
	for _, path := range []string{
		"task/ready", "task/revision_list", "task/revision_read", "task/correction_create", "task/supersede", "task/submit-tests",
		"task/_ready", "task/_revision_list", "task/_revision_read", "task/_correction_create", "task/_supersede",
		"task/work", "task/finalize",
	} {
		if _, ok := entries[path]; ok {
			t.Fatalf("retired Task action remains registered: %q", path)
		}
	}
	if _, ok := entries["task/dispatch"]; !ok {
		t.Fatal("TSK521 task/dispatch execution action disappeared")
	}
	if _, ok := entries["task/integrate"]; !ok {
		t.Fatal("TSK521 task/integrate execution action disappeared")
	}

	assertSchemaKeys := func(path string, schema map[string]any, want []string) {
		t.Helper()
		got := schemaProperties(schema)
		if len(got) != len(want) {
			t.Fatalf("%s properties=%v", path, got)
		}
		for _, key := range want {
			if _, ok := got[key]; !ok {
				t.Fatalf("%s missing property %q: %v", path, key, got)
			}
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s is not closed: %#v", path, schema)
		}
	}
	assertRequired := func(path string, schema map[string]any, want ...string) {
		t.Helper()
		got := stringList(schema["required"])
		for _, field := range want {
			found := false
			for _, candidate := range got {
				if candidate == field {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s missing required field %q: %v", path, field, got)
			}
		}
	}
	assertSchemaKeys("task/create", entries["task/create"].InputSchema, []string{"title", "summary", "objective", "adr_relation", "adr_references", "type", "scope", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "relation_type", "relation_target"})
	assertRequired("task/create", entries["task/create"].InputSchema, "title", "summary", "objective", "adr_relation")
	assertSchemaKeys("task/read", entries["task/read"].InputSchema, []string{"key", "revision"})
	assertRequired("task/read", entries["task/read"].InputSchema, "key")
	assertSchemaKeys("task/update", entries["task/update"].InputSchema, []string{"key", "reason", "title", "summary", "objective", "adr_relation", "adr_references", "type", "scope", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata"})
	assertRequired("task/update", entries["task/update"].InputSchema, "key", "reason")
	assertSchemaKeys("task/list", entries["task/list"].InputSchema, []string{"cursor", "include_archived"})
	assertSchemaKeys("task/query", entries["task/query"].InputSchema, []string{"cursor", "text", "status", "type"})
	assertSchemaKeys("task/archive", entries["task/archive"].InputSchema, []string{"key", "reason"})
	assertRequired("task/archive", entries["task/archive"].InputSchema, "key", "reason")
	assertSchemaKeys("task/history", entries["task/history"].InputSchema, []string{"key", "cursor"})
	assertRequired("task/history", entries["task/history"].InputSchema, "key")
	for _, path := range []string{"task/create", "task/update", "task/archive"} {
		assertSchemaKeys(path+" output", entries[path].OutputSchema, []string{"key", "revision"})
	}
	for _, path := range []string{"task/list", "task/query"} {
		assertSchemaKeys(path+" output", entries[path].OutputSchema, []string{"items"})
		items := entries[path].OutputSchema["properties"].(map[string]any)["items"].(map[string]any)
		item := items["items"].(map[string]any)
		assertSchemaKeys(path+" item", item, []string{"key", "title", "summary", "status", "revision", "updated_at"})
		assertRequired(path+" item", item, "key", "title", "summary", "status", "revision")
		for _, field := range stringList(item["required"]) {
			if field == "updated_at" {
				t.Fatalf("%s made updated_at required", path)
			}
		}
		if _, ok := schemaProperties(item)["detail"]; ok {
			t.Fatalf("%s injected detail into compact items", path)
		}
	}
	assertSchemaKeys("task/read output", entries["task/read"].OutputSchema, []string{"key", "revision", "title", "summary", "status", "type", "scope", "objective", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "adr_relation", "adr_references", "relations", "created_at", "updated_at"})
	if _, ok := schemaProperties(entries["task/read"].OutputSchema)["detail"]; ok {
		t.Fatal("task/read output has projection detail")
	}

	for _, path := range []string{"debug/adr_legacy_relations", "debug/task_legacy_revision_list", "debug/task_legacy_revision_read"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("retired legacy debug action remains registered: %q", path)
		}
	}
}

func TestTSK531TaskHistoryOutputIsUniversalKeyAndItems(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	schema := entries["task/history"].OutputSchema
	properties := schemaProperties(schema)
	if len(properties) != 2 {
		t.Fatalf("task/history output properties=%v", properties)
	}
	for _, key := range []string{"key", "items"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("task/history output missing %q: %v", key, properties)
		}
	}
	for _, alias := range []string{"revisions", "task", "task_id", "task_key", "id"} {
		if _, ok := properties[alias]; ok {
			t.Fatalf("task/history output exposed the retired %q alias: %v", alias, properties)
		}
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("task/history output is not closed: %#v", schema)
	}
	required := stringList(schema["required"])
	if len(required) != 2 || required[0] != "items" || required[1] != "key" {
		t.Fatalf("task/history required=%v", required)
	}
	row := properties["items"].(map[string]any)["items"].(map[string]any)
	rowProperties := schemaProperties(row)
	if len(rowProperties) != 6 {
		t.Fatalf("task/history item properties=%v", rowProperties)
	}
	for _, key := range []string{"revision", "mutation_kind", "actor", "reason", "changed_fields", "recorded_at"} {
		if _, ok := rowProperties[key]; !ok {
			t.Fatalf("task/history item missing %q: %v", key, rowProperties)
		}
	}
	for _, alias := range []string{"id", "task", "task_id", "task_key", "key"} {
		if _, ok := rowProperties[alias]; ok {
			t.Fatalf("task/history item exposed the retired %q alias: %v", alias, rowProperties)
		}
	}
}

func TestTSK531TaskHistoryReadsSharedLifecycleAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	server.AuthorityContext = authority.WithPlanner(context.Background())
	sessionID := genericSession(t, server.Service, "example")
	call := func(id int, action string, input map[string]any) map[string]any {
		t.Helper()
		return genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "call", "arguments": map[string]any{
				"session_id": sessionID, "action": action, "input": input,
			}},
		})))
	}
	created := call(1, "task/create", map[string]any{
		"title": "Shared history Task", "summary": "Shared history summary.", "objective": "Read shared history.", "adr_relation": "no_adr_required",
	})
	key := created["result"].(map[string]any)["key"].(string)
	if _, err := server.Service.TaskLifecycleArchive(context.Background(), "example", key, "planner", "retire"); err != nil {
		t.Fatal(err)
	}
	history := call(2, "task/history", map[string]any{"key": key})
	result := history["result"].(map[string]any)
	if result["key"] != key {
		t.Fatalf("task/history key=%#v", result)
	}
	items, ok := result["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("task/history items=%#v", result)
	}
	if _, ok := result["revisions"]; ok {
		t.Fatalf("task/history leaked the retired revisions alias: %#v", result)
	}
	last := items[len(items)-1].(map[string]any)
	if last["mutation_kind"] != "archive" || last["revision"] != float64(1) {
		t.Fatalf("task/history archive row=%#v", last)
	}
	rows, err := server.Service.Durability.ListTaskLifecycleEvents(context.Background(), "example", key, 64)
	if err != nil || len(rows) != 1 || rows[0].EventKind != "archive" {
		t.Fatalf("shared task lifecycle authority=%#v err=%v", rows, err)
	}
}

func TestTSK531LegacyRevisionHelpersRemainAvailableWithoutPublicActions(t *testing.T) {
	server := newSessionTestServer(t)
	ctx := authority.WithPlanner(context.Background())
	hubRevision, err := server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := server.Service.TaskCreate(ctx, service.TaskCreateInput{
		ProjectID: "example", Slug: "legacy-evidence-fixture", Title: "Legacy evidence fixture",
		Objective: "Preserve one legacy revision for evidence.", AcceptanceCriteria: []string{"read-only"},
		CreatedBy: "planner", WriteOptions: service.WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := server.Service.TaskRevisionLegacyEvidenceListPage(ctx, task.ID, service.CollectionPageInput{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Revisions) != 1 || page.Revisions[0].TaskID != task.ID {
		t.Fatalf("legacy revisions=%#v", page.Revisions)
	}
	read, err := server.Service.TaskRevisionLegacyEvidenceRead(ctx, page.Revisions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.ID != page.Revisions[0].ID || read.TaskID != task.ID {
		t.Fatalf("legacy read=%#v", read)
	}
	if _, err := server.Service.TaskRevisionLegacyEvidenceRead(ctx, fmt.Sprintf("%s.REV0", task.ID)); err == nil {
		t.Fatal("invalid legacy REV identifier was accepted")
	}
}
