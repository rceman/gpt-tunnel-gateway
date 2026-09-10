package mcp

import (
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK567TaskReadAgentContractIsBoundedAndReadOnly(t *testing.T) {
	server := newSessionTestServer(t)
	entry, ok := server.genericActionRegistry(server.tools())["task/read"]
	if !ok {
		t.Fatal("task/read is not registered")
	}
	if entry.AuthorityRole != actionRolePlannerOrAgent || !entry.SessionBound || !entry.LocalReceiptOnly || !entry.Annotations.ReadOnlyHint || !entry.Annotations.IdempotentHint {
		t.Fatalf("task/read authority=%#v", entry)
	}
	if !actionAuthorityAllowsSessionRole(entry.AuthorityRole, durableSession.RoleAgent) {
		t.Fatal("task/read is not available to a bound Agent session")
	}
	properties := schemaProperties(entry.OutputSchema)
	for _, field := range []string{"key", "revision", "title", "summary", "status", "type", "scope", "objective", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "adr_relation", "adr_references", "created_at", "updated_at"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("task/read output missing %q: %#v", field, properties)
		}
	}
	if entry.OutputSchema["additionalProperties"] != false {
		t.Fatalf("task/read output is not closed: %#v", entry.OutputSchema)
	}
	if _, ok := properties["detail"]; ok {
		t.Fatal("task/read output exposes projection detail")
	}
}
