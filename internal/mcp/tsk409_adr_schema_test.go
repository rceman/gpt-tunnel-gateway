package mcp

import (
	"reflect"
	"testing"
)

func TestTSK409ADRPublicSchemasAreClosedAndTransportNeutral(t *testing.T) {
	server := newSessionTestServer(t)
	server.ensureADRActions()
	entries := server.genericActionRegistry(nil)
	want := map[string][]string{
		"adr/create":                 {"title", "context", "decision", "consequences"},
		"adr/read":                   {"adr", "revision"},
		"adr/update":                 {"adr", "reason", "title", "context", "decision", "consequences", "status"},
		"adr/list":                   {"cursor", "include_archived"},
		"adr/query":                  {"cursor", "text", "status"},
		"adr/archive":                {"adr", "reason"},
		"adr/history":                {"adr", "cursor"},
		"debug/adr_legacy_relations": {"adr", "cursor"},
	}
	for path, fields := range want {
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("missing action %s", path)
		}
		assertTSK409ClosedSchema(t, path+" input", entry.InputSchema, fields)
		forbidden := []string{"project", "project_id", "actor", "limit", "page_size", "expected_revision", "supersedes", "replaced_by", "_pagination", "_metrics"}
		for _, name := range forbidden {
			if _, exists := tsk409SchemaProperties(entry.InputSchema)[name]; exists {
				t.Fatalf("%s exposes forbidden input %q", path, name)
			}
			if _, exists := tsk409SchemaProperties(entry.OutputSchema)[name]; exists {
				t.Fatalf("%s exposes forbidden output %q", path, name)
			}
		}
		if props := tsk409SchemaProperties(entry.OutputSchema); props != nil {
			if _, ok := props["_pagination"]; ok {
				t.Fatalf("%s output exposes private pagination", path)
			}
		}
	}
	for _, path := range []string{"adr/create", "adr/update", "adr/archive"} {
		props := tsk409SchemaProperties(entries[path].OutputSchema)
		if !reflect.DeepEqual(tsk409SortedSchemaKeys(props), []string{"adr", "revision"}) {
			t.Fatalf("%s output properties=%v", path, tsk409SortedSchemaKeys(props))
		}
	}
	queryStatus := tsk409SchemaProperties(entries["adr/query"].InputSchema)["status"].(map[string]any)
	if !reflect.DeepEqual(queryStatus["enum"], []any{"accepted", "superseded", "archived"}) {
		t.Fatalf("query status enum=%#v", queryStatus["enum"])
	}
}

func assertTSK409ClosedSchema(t *testing.T, name string, schema map[string]any, fields []string) {
	t.Helper()
	if schema["additionalProperties"] != false {
		t.Fatalf("%s is not closed: %#v", name, schema)
	}
	got := tsk409SortedSchemaKeys(tsk409SchemaProperties(schema))
	want := append([]string(nil), fields...)
	tsk409SortStrings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields=%v want=%v", name, got, want)
	}
}

func tsk409SchemaProperties(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	return props
}

func tsk409SortedSchemaKeys(props map[string]any) []string {
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	tsk409SortStrings(keys)
	return keys
}

func tsk409SortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
