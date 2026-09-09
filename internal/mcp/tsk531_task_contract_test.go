package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
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
	assertSchemaKeys("task/create", entries["task/create"].InputSchema, []string{"title", "summary", "objective", "adr_relation", "adr_references", "type", "scope", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata"})
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
		assertSchemaKeys(path+" output", entries[path].OutputSchema, []string{"items", "next_cursor"})
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
	legacyFields := []string{"schema_version", "id", "task_id", "task_revision", "revision_sha256", "parent_task_revision", "parent_task_sha256", "project_id", "title", "type", "objective", "branch", "base_revision", "acceptance_criteria", "constraints", "required_gates", "workflow_policy_revision", "operation_class", "effective_ci_field", "effective_ci_mode", "wait_for_ci", "ci_blocking", "agent_may_wait", "status", "source_train_id", "source_item_position", "source_attempt_number", "source_run_id", "source_report_id", "created_by", "created_at"}
	if legacyProjection["additionalProperties"] != false || len(schemaProperties(legacyProjection)) != len(legacyFields) {
		t.Fatalf("legacy read projection is not closed/full: %#v", legacyProjection)
	}
	for _, field := range legacyFields {
		if _, ok := schemaProperties(legacyProjection)[field]; !ok {
			t.Fatalf("legacy read projection missing %q", field)
		}
	}
}

func TestTSK531LegacyRevisionActionsExecuteAgainstBoundedTempHub(t *testing.T) {
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
	entries := server.genericActionRegistry(server.tools())
	listValue, err := entries["debug/task_legacy_revision_list"].Execute(ctx, mustJSON(t, map[string]any{
		"project_id": "example", "task": task.ID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := listValue.(map[string]any)
	if !ok || list["task"] != task.ID {
		t.Fatalf("legacy list=%#v", listValue)
	}
	revisions, ok := list["revisions"].([]any)
	if !ok || len(revisions) != 1 {
		t.Fatalf("legacy revisions=%#v", list["revisions"])
	}
	revisionID := revisions[0].(map[string]any)["revision_id"].(string)
	readValue, err := entries["debug/task_legacy_revision_read"].Execute(ctx, mustJSON(t, map[string]any{
		"project_id": "example", "revision_id": revisionID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	read, ok := readValue.(map[string]any)
	if !ok || read["revision"] == nil {
		t.Fatalf("legacy read=%#v", readValue)
	}
	if _, err := entries["debug/task_legacy_revision_list"].Execute(ctx, mustJSON(t, map[string]any{
		"project_id": "other", "task": task.ID,
	})); err == nil {
		t.Fatal("legacy list crossed project ownership")
	}
	if _, err := entries["debug/task_legacy_revision_read"].Execute(ctx, mustJSON(t, map[string]any{
		"project_id": "example", "revision_id": fmt.Sprintf("%s.REV0", task.ID),
	})); err == nil {
		t.Fatal("invalid legacy REV identifier was accepted")
	}
}
