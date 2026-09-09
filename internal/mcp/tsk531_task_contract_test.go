package mcp

import (
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK531CanonicalTaskSurfaceAndLegacyEvidenceContract(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())

	wantCanonical := map[string]bool{
		"task/create": true, "task/read": true, "task/update": true,
		"task/list": true, "task/query": true, "task/archive": true, "task/history": true,
	}
	for path := range wantCanonical {
		if _, ok := entries[path]; !ok {
			t.Fatalf("missing canonical Task action %q", path)
		}
	}
	for _, path := range []string{
		"task/ready", "task/revision_list", "task/revision_read", "task/correction_create", "task/supersede",
		"task/_ready", "task/_revision_list", "task/_revision_read", "task/_correction_create", "task/_supersede",
	} {
		if _, ok := entries[path]; ok {
			t.Fatalf("retired Task action remains registered: %q", path)
		}
	}
	if _, ok := entries["task/work"]; !ok {
		t.Fatal("TSK521 task/work execution action disappeared")
	}
	if _, ok := entries["task/finalize"]; !ok {
		t.Fatal("TSK521 task/finalize execution action disappeared")
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
	assertSchemaKeys("task/create", entries["task/create"].InputSchema, []string{"title", "summary", "objective", "adr_relation", "type", "scope", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata"})
	assertSchemaKeys("task/read", entries["task/read"].InputSchema, []string{"key", "revision"})
	assertSchemaKeys("task/update", entries["task/update"].InputSchema, []string{"key", "reason", "title", "summary", "objective", "adr_relation", "type", "scope", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata"})
	assertSchemaKeys("task/list", entries["task/list"].InputSchema, []string{"cursor", "include_archived"})
	assertSchemaKeys("task/query", entries["task/query"].InputSchema, []string{"cursor", "text", "status", "type"})
	assertSchemaKeys("task/archive", entries["task/archive"].InputSchema, []string{"key", "reason"})
	assertSchemaKeys("task/history", entries["task/history"].InputSchema, []string{"key", "cursor"})
	for _, path := range []string{"task/create", "task/update", "task/archive"} {
		assertSchemaKeys(path+" output", entries[path].OutputSchema, []string{"key", "revision"})
	}
	for _, path := range []string{"task/list", "task/query"} {
		assertSchemaKeys(path+" output", entries[path].OutputSchema, []string{"items", "next_cursor"})
		if _, ok := schemaProperties(entries[path].OutputSchema["properties"].(map[string]any)["items"].(map[string]any))["detail"]; ok {
			t.Fatalf("%s injected detail into compact items", path)
		}
	}
	assertSchemaKeys("task/read output", entries["task/read"].OutputSchema, []string{"key", "revision", "title", "summary", "status", "type", "scope", "objective", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "adr_relation", "adr_references", "created_at", "updated_at"})
	if _, ok := schemaProperties(entries["task/read"].OutputSchema)["detail"]; ok {
		t.Fatal("task/read output has projection detail")
	}

	for _, path := range []string{"debug/task_legacy_revision_list", "debug/task_legacy_revision_read"} {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing legacy evidence action %q", path)
		}
		if entry.AuthorityRole != durableSession.RolePlanner || !entry.SessionBound || !entry.SessionRequired || !entry.LocalReadOnly || !entry.LocalReceiptOnly || !entry.Annotations.ReadOnlyHint || !entry.Annotations.IdempotentHint {
			t.Fatalf("%s authority/annotations=%#v", path, entry)
		}
		if actionAuthorityAllowsSessionRole(entry.AuthorityRole, durableSession.RoleAgent) {
			t.Fatalf("Agent access allowed for %s", path)
		}
	}
	list := entries["debug/task_legacy_revision_list"]
	assertSchemaKeys("legacy list", list.InputSchema, []string{"task", "cursor"})
	if _, ok := schemaProperties(list.InputSchema)["limit"]; ok {
		t.Fatal("legacy list exposes caller limit")
	}
	assertSchemaKeys("legacy list output", list.OutputSchema, []string{"task", "revisions", "next_cursor"})
	if taskLegacyRevisionPageSize != 20 {
		t.Fatalf("legacy page size=%d", taskLegacyRevisionPageSize)
	}

	read := entries["debug/task_legacy_revision_read"]
	assertSchemaKeys("legacy read", read.InputSchema, []string{"revision_id"})
	readProperties := schemaProperties(read.OutputSchema)
	legacyProjection, ok := readProperties["revision"].(map[string]any)
	if !ok {
		t.Fatalf("legacy read projection=%#v", read.OutputSchema)
	}
	if legacyProjection["additionalProperties"] != false || len(schemaProperties(legacyProjection)) != 30 {
		t.Fatalf("legacy read projection is not closed/full: %#v", legacyProjection)
	}
}
