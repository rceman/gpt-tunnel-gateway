package mcp

import (
	"reflect"
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK578RoleRegistryFeedsPublicSchemasAndProjections(t *testing.T) {
	want := durableSession.WorkflowRoleNames()
	wantAny := make([]any, len(want))
	for i, role := range want {
		wantAny[i] = role
	}

	server := newSessionTestServer(t)
	start, ok := server.actionContractSet().Action("session/start")
	if !ok {
		t.Fatal("compiled session/start contract is missing")
	}
	startProperties := start.Input.JSONSchema()["properties"].(map[string]any)
	if len(startProperties) != 1 || startProperties["token"] == nil {
		t.Fatalf("session_start bootstrap schema=%#v", startProperties)
	}
	if got := start.Output.JSONSchema()["properties"].(map[string]any)["role"].(map[string]any)["enum"]; !reflect.DeepEqual(got, wantAny) {
		t.Fatalf("session start role enum=%#v want registry=%#v", got, wantAny)
	}
	guideRole := guidePublicOutputSchema()["properties"].(map[string]any)["roles"].(map[string]any)
	if guideRole["type"] != "array" {
		t.Fatalf("guide role projection schema=%#v", guideRole)
	}
	guideItems := guideRole["items"].(map[string]any)
	guideKey := guideItems["properties"].(map[string]any)["key"].(map[string]any)
	if got := guideKey["enum"]; !reflect.DeepEqual(got, wantAny) {
		t.Fatalf("guide role enum=%#v want registry=%#v", got, wantAny)
	}
	for _, role := range durableSession.WorkflowRoles() {
		if !durableSession.IsWorkflowRole(role.Key) || role.Code == "" {
			t.Fatalf("registry role is not authoritative: %#v", role)
		}
	}
}

func TestTSK578DurableSessionResolutionUsesCanonicalSessionBindingNotIDPrefix(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker, durableSession.RoleLead}, true, true)
	workerID := fixture.sessions[durableSession.RoleWorker]
	if len(workerID) != 15 || workerID[:8] != "HOM_EXM_" || workerID[8] != 'W' {
		t.Fatalf("unexpected canonical Worker Session ID=%q", workerID)
	}
	result := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/read", map[string]any{"key": fixture.task.ID})
	if result["ok"] != true {
		t.Fatalf("runtime did not resolve canonical Worker Session: %#v", result)
	}
}
