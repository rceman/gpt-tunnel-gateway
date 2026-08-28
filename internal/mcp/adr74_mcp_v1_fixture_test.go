package mcp

import (
	"net/http/httptest"
	"sort"
	"testing"
)

// adr74MCPV1Fixture is the protected transport contract. Application actions
// remain discoverable through schema and are intentionally absent here.
var adr74MCPV1Fixture = map[string]struct {
	required   []string
	properties map[string]string
}{
	"status":        {required: []string{}, properties: map[string]string{}},
	"session_start": {required: []string{"role"}, properties: map[string]string{"role": "string", "label": "string"}},
	"schema":        {required: []string{}, properties: map[string]string{"path": "string"}},
	"call": {required: []string{"session", "action", "input"}, properties: map[string]string{
		"session": "string", "action": "string", "input": "object",
	}},
	"batch": {required: []string{"session", "calls"}, properties: map[string]string{
		"session": "string", "calls": "array",
	}},
}

func TestADR74MCPV1ProtectedInputFixture(t *testing.T) {
	server := newSessionTestServer(t)
	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()
	client := &frozenConnectorClient{http: httpServer.Client(), endpoint: httpServer.URL + "/mcp", methods: map[string]int{}}
	tools := client.request(t, "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(adr74MCPV1Fixture) {
		t.Fatalf("protected tool count=%d want=%d", len(tools), len(adr74MCPV1Fixture))
	}
	seen := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		fixture, ok := adr74MCPV1Fixture[name]
		if !ok {
			t.Fatalf("unexpected protected tool %q", name)
		}
		seen = append(seen, name)
		assertADR74InputSchema(t, name, tool["inputSchema"].(map[string]any), fixture)
	}
	sort.Strings(seen)
	want := []string{"batch", "call", "schema", "session_start", "status"}
	if len(seen) != len(want) {
		t.Fatalf("protected tools=%v want=%v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("protected tools=%v want=%v", seen, want)
		}
	}
}

func assertADR74InputSchema(t *testing.T, name string, schema map[string]any, fixture struct {
	required   []string
	properties map[string]string
}) {
	t.Helper()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("%s input envelope=%#v", name, schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) != len(fixture.properties) {
		t.Fatalf("%s properties=%#v", name, schema["properties"])
	}
	for property, wantType := range fixture.properties {
		value, ok := properties[property].(map[string]any)
		if !ok || value["type"] != wantType {
			t.Fatalf("%s.%s schema=%#v", name, property, value)
		}
	}
	for property := range properties {
		if _, ok := fixture.properties[property]; !ok {
			t.Fatalf("%s has unexpected property %q", name, property)
		}
	}
	got := []any{}
	if raw, present := schema["required"]; present {
		var ok bool
		got, ok = raw.([]any)
		if !ok {
			t.Fatalf("%s required=%#v want=%v", name, raw, fixture.required)
		}
	}
	if len(got) != len(fixture.required) {
		t.Fatalf("%s required=%#v want=%v", name, got, fixture.required)
	}
	for i, property := range fixture.required {
		if got[i] != property {
			t.Fatalf("%s required=%#v want=%v", name, got, fixture.required)
		}
	}
	if name == "batch" {
		calls := properties["calls"].(map[string]any)
		if calls["maxItems"] != float64(genericBatchMaxItems) {
			t.Fatalf("batch maxItems=%#v", calls["maxItems"])
		}
		item := calls["items"].(map[string]any)
		assertADR74InputSchema(t, "batch item", item, struct {
			required   []string
			properties map[string]string
		}{required: []string{"action", "input"}, properties: map[string]string{"action": "string", "input": "object"}})
	}
}
