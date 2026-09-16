package mcp

import (
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK631TaskRefreshHasClosedLeadPlannerContract(t *testing.T) {
	server := newSessionTestServer(t)
	entry, ok := server.genericActionRegistry(server.tools())["task/refresh"]
	if !ok {
		t.Fatal("task/refresh is not registered")
	}
	if entry.AuthorityRole != actionRolePlannerOrLead || !entry.SessionBound || !entry.LocalReceiptOnly {
		t.Fatalf("task/refresh authority=%#v", entry)
	}
	for _, role := range []string{durableSession.RoleWorker, durableSession.RoleAdvisor} {
		if actionAuthorityAllowsSessionRole(entry.AuthorityRole, role) {
			t.Fatalf("task/refresh admitted %s", role)
		}
	}
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead} {
		if !actionAuthorityAllowsSessionRole(entry.AuthorityRole, role) {
			t.Fatalf("task/refresh rejected %s", role)
		}
	}
	for name, candidate := range map[string]struct {
		schema map[string]any
		count  int
	}{"input": {schema: entry.InputSchema, count: 2}, "execution input": {schema: entry.ExecutionInputSchema, count: 3}} {
		schema := candidate.schema
		if schema["additionalProperties"] != false {
			t.Fatalf("%s schema is not closed: %#v", name, schema)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok || len(properties) != candidate.count {
			t.Fatalf("%s properties=%#v", name, schema)
		}
		reason, ok := properties["reason"].(map[string]any)
		if !ok || reason["minLength"] != 1 || reason["maxLength"] != 1024 {
			t.Fatalf("%s reason schema=%#v", name, reason)
		}
	}
	properties, ok := entry.OutputSchema["properties"].(map[string]any)
	if !ok || properties["reason"] == nil || entry.OutputSchema["additionalProperties"] != false {
		t.Fatalf("task/refresh output=%#v", entry.OutputSchema)
	}
}
