package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func installTSK563Airelay(t *testing.T, fixture *tsk571HTTPFixture) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airelay")
	script := `#!/bin/sh
case "$1" in
session-status)
  if [ "$3" = "--json" ]; then
    printf '{"sessionKey":"%s","profile":"coding","controllerReachable":true,"state":"idle"}' "$2"
  else
    printf 'Controller: reachable\nState: idle\n'
  fi
  ;;
tail) printf 'runtime-line\n' ;;
prompt) exit 0 ;;
*) exit 0 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.server.Service.Airelay.Command = path
	fixture.server.Service.Config.AirelayCommand = path
	fixture.server.Service.Airelay.Timeout = time.Second
}

func TestTSK563GenericAgentControlsReachEveryManagedRole(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	for _, role := range []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		t.Run(role, func(t *testing.T) {
			fixture := newTSK571HTTPFixture(t, []string{role}, true, true)
			installTSK563Airelay(t, fixture)

			for _, action := range []struct {
				name  string
				input map[string]any
			}{
				{name: "agent/status", input: map[string]any{}},
				{name: "agent/await", input: map[string]any{"seconds": 1}},
				{name: "agent/tail", input: map[string]any{"lines": 1}},
				{name: "agent/prompt", input: map[string]any{"message": "TSK563 runtime control"}},
			} {
				result := fixture.call(t, fixture.runtime, action.name, action.input)
				if result["ok"] != true {
					t.Fatalf("%s was not reachable for %s runtime: %#v", action.name, role, result)
				}
			}
		})
	}
}

func TestTSK563SharedAgentKeepsRoleSessionsDistinct(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)

	for _, role := range []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		resolved, err := fixture.server.Service.ResolveRuntimeRoleSessionForSession(context.Background(), fixture.runtime, fixture.sessions[role])
		if err != nil {
			t.Fatalf("resolve %s Session: %v", role, err)
		}
		if resolved.Session.ID != fixture.sessions[role] || resolved.Session.Role != role || resolved.Agent.AgentID != fixture.agentID {
			t.Fatalf("shared Agent merged %s authority: %#v", role, resolved)
		}
		result := fixture.call(t, fixture.runtime, "agent/tail", map[string]any{"session": fixture.sessions[role], "lines": 1})
		if result["ok"] != true {
			t.Fatalf("explicit %s Session tail failed: %#v", role, result)
		}
	}

	for _, role := range []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		result := fixture.call(t, fixture.sessions[role], "agent/status", map[string]any{})
		message := tsk571ErrorMessage(t, result)
		if !strings.Contains(message, "managed runtime identity is required") {
			t.Fatalf("direct %s Session acquired generic runtime authority: %q", role, message)
		}
	}
}

func TestTSK563AgentTailExplicitSessionRejectsMismatchedBinding(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead, durableSession.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)
	other := fixture.addSession(t, "other", "OTH", durableSession.RoleWorker, fixture.runtime)

	for _, sessionID := range []string{other, "HOM_EXM_W_zzzzz"} {
		result := fixture.call(t, fixture.runtime, "agent/tail", map[string]any{"session": sessionID, "lines": 1})
		message := tsk571ErrorMessage(t, result)
		if !strings.Contains(message, "managed runtime is not authorized for the requested Agent Session") {
			t.Fatalf("mismatched Session %q was not rejected: %q", sessionID, message)
		}
	}
}

func TestTSK563TaskDispatchUsesAttachedWorkerAndHidesAgentSelector(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)

	entry, ok := fixture.server.genericActionRegistry(fixture.server.tools())["task/dispatch"]
	if !ok {
		t.Fatal("task/dispatch is not registered")
	}
	properties, ok := entry.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("task/dispatch schema has no properties: %#v", entry.InputSchema)
	}
	if _, exists := properties["agent"]; exists {
		t.Fatalf("task/dispatch still exposes caller-selected Agent: %#v", entry.InputSchema)
	}

	dispatched := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "task/dispatch", map[string]any{"key": fixture.task.ID})
	if dispatched["ok"] != true {
		t.Fatalf("Task dispatch did not resolve the attached Worker: %#v", dispatched)
	}
	if agent, _ := dispatched["result"].(map[string]any); agent["agent"] != fixture.agentID {
		t.Fatalf("Task dispatch used unexpected runtime: %#v", dispatched)
	}

	unknownSelector := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "task/dispatch", map[string]any{"key": fixture.task.ID, "agent": "other-agent"})
	message := tsk571ErrorMessage(t, unknownSelector)
	if !strings.Contains(message, "unknown argument") || !strings.Contains(message, "agent") {
		t.Fatalf("caller-selected Agent field was accepted: %q", message)
	}
}

func TestTSK563TaskDispatchFailsClosedOnAmbiguousAttachedWorker(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	installTSK563Airelay(t, fixture)
	fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, fixture.runtime)

	result := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "task/dispatch", map[string]any{"key": fixture.task.ID})
	message := tsk571ErrorMessage(t, result)
	if !strings.Contains(message, "RUNTIME_SESSION_AMBIGUOUS") {
		t.Fatalf("ambiguous attached Worker did not fail closed: %q", message)
	}
}

func TestTSK563AgentAndSupervisorAreNotWorkflowRoles(t *testing.T) {
	if durableSession.IsWorkflowRole("agent") || durableSession.IsWorkflowRole("supervisor") {
		t.Fatal("legacy Agent/Supervisor compatibility role is registered")
	}
	if err := authority.RequireRole(context.Background(), "agent"); err == nil {
		t.Fatal("Agent acquired workflow-role authority")
	}
	if err := authority.RequireRole(context.Background(), "supervisor"); err == nil {
		t.Fatal("Supervisor acquired workflow-role authority")
	}
	server := newSessionTestServer(t)
	for _, role := range []string{"agent", "supervisor"} {
		ref := "runtime-tsk563"
		if _, err := mcpSQLiteSessionStore(t, server.Service).Create(durableSession.CreateInput{
			ProjectID: "example", ProjectCode: "EXM", Role: role, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
		}); err == nil {
			t.Fatalf("%s compatibility Session was accepted", role)
		}
	}
}
