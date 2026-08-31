package mcp

import (
	"net/http/httptest"
	"sort"
	"testing"
)

// adr84MCPV1Fixture is the protected transport contract. Application actions
// remain discoverable through schema and are intentionally absent here.
var adr84MCPV1Fixture = map[string]struct {
	required   []string
	properties map[string]string
}{
	"status":        {required: []string{}, properties: map[string]string{}},
	"guide":         {required: []string{}, properties: map[string]string{}},
	"projects":      {required: []string{"gateway"}, properties: map[string]string{"gateway": "string"}},
	"session_start": {required: []string{"gateway", "project", "role"}, properties: map[string]string{"gateway": "string", "project": "string", "role": "string", "ref": "string"}},
	"schema":        {required: []string{"session"}, properties: map[string]string{"session": "string", "path": "string"}},
	"call": {required: []string{"session", "action", "input"}, properties: map[string]string{
		"session": "string", "action": "string", "input": "object",
	}},
}

func TestADR84MCPV1ProtectedInputFixture(t *testing.T) {
	server := newSessionTestServer(t)
	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()
	client := &frozenConnectorClient{http: httpServer.Client(), endpoint: httpServer.URL + "/mcp", methods: map[string]int{}}
	tools := client.request(t, "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(adr84MCPV1Fixture) {
		t.Fatalf("protected tool count=%d want=%d", len(tools), len(adr84MCPV1Fixture))
	}
	seen := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		fixture, ok := adr84MCPV1Fixture[name]
		if !ok {
			t.Fatalf("unexpected protected tool %q", name)
		}
		seen = append(seen, name)
		assertADR84InputSchema(t, name, tool["inputSchema"].(map[string]any), fixture)
	}
	sort.Strings(seen)
	want := []string{"call", "guide", "projects", "schema", "session_start", "status"}
	if len(seen) != len(want) {
		t.Fatalf("protected tools=%v want=%v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("protected tools=%v want=%v", seen, want)
		}
	}
}

func assertADR84InputSchema(t *testing.T, name string, schema map[string]any, fixture struct {
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
}
