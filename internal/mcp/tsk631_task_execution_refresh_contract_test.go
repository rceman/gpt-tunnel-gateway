package mcp

import (
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK631TaskRefreshHasClosedLeadContract(t *testing.T) {
	server := newSessionTestServer(t)
	entry, ok := server.genericActionRegistry(server.tools())["task/refresh"]
	if !ok {
		t.Fatal("task/refresh is not registered")
	}
	if entry.AuthorityRole != durableSession.RoleLead || !entry.SessionBound || !entry.LocalReceiptOnly {
		t.Fatalf("task/refresh authority=%#v", entry)
	}
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		if !actionAuthorityAllowsSessionRole(entry.AuthorityRole, role) {
			t.Fatalf("task/refresh rejected %s", role)
		}
	}
	if entry.InputSchema["additionalProperties"] != false || !entry.InjectSessionProjectID {
		t.Fatalf("task/refresh public input or session project binding is incomplete: %#v", entry)
	}
	properties, ok := entry.InputSchema["properties"].(map[string]any)
	if !ok || len(properties) != 2 {
		t.Fatalf("task/refresh properties=%#v", entry.InputSchema)
	}
	reason, ok := properties["reason"].(map[string]any)
	if !ok || reason["minLength"] != 1 || reason["maxLength"] != 1024 {
		t.Fatalf("task/refresh reason schema=%#v", reason)
	}
	properties, ok = entry.OutputSchema["properties"].(map[string]any)
	if !ok || properties["reason"] == nil || entry.OutputSchema["additionalProperties"] != false {
		t.Fatalf("task/refresh output=%#v", entry.OutputSchema)
	}
}
