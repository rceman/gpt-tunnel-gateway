package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK600GenericRuntimeAmbiguityFailsClosed(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK571Agent(t, fixture.server.Service, revision, "coding-secondary", true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-secondary"] = config.AgentBinding{SessionKey: fixture.runtime, Profile: "coding"}

	result := fixture.call(t, fixture.runtime, "agent/status", map[string]any{})
	message := tsk571ErrorMessage(t, result)
	if !strings.Contains(message, "RUNTIME_IDENTITY_AMBIGUOUS") {
		t.Fatalf("ambiguous managed runtime was not rejected: %q", message)
	}
}

func TestTSK600DistinctLeadWorkerRuntimesKeepGenericAndTaskRoutingSeparate(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	workerRuntime := "runtime-tsk600-worker"
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK571Agent(t, fixture.server.Service, revision, "coding-worker", true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-worker"] = config.AgentBinding{SessionKey: workerRuntime, Profile: "coding"}
	workerSession := fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, workerRuntime)
	planner := fixture.sessions[durableSession.RolePlanner]

	for _, runtime := range []string{fixture.runtime, workerRuntime} {
		status := fixture.call(t, runtime, "agent/status", map[string]any{})
		if status["ok"] != true {
			t.Fatalf("generic agent/status failed for runtime %s: %#v", runtime, status)
		}
	}
	plannerWorkerStatus := fixture.call(t, planner, "agent/status", map[string]any{"agent": "coding-worker"})
	if plannerWorkerStatus["ok"] != true {
		t.Fatalf("Planner could not address the Worker through generic agent/status: %#v", plannerWorkerStatus)
	}

	dispatched := fixture.call(t, planner, "task/dispatch", map[string]any{"key": fixture.task.ID})
	if dispatched["ok"] != true {
		t.Fatalf("Task dispatch failed with distinct Lead and Worker Agents: %#v", dispatched)
	}
	dispatchResult, ok := dispatched["result"].(map[string]any)
	if !ok || dispatchResult["agent"] != "coding-worker" {
		t.Fatalf("Task dispatch did not derive the attached Worker: %#v", dispatched)
	}
	workerRead := fixture.call(t, workerRuntime, "task/read", map[string]any{"key": fixture.task.ID})
	if workerRead["ok"] != true {
		t.Fatalf("Worker runtime lost Task authority: %#v", workerRead)
	}
	leadWorkerAction := fixture.call(t, fixture.runtime, "task/current", map[string]any{})
	if message := tsk571ErrorMessage(t, leadWorkerAction); !strings.Contains(message, "RUNTIME_SESSION_UNAVAILABLE") {
		t.Fatalf("Lead runtime acquired Worker lane authority: %q", message)
	}
	workerLeadAction := fixture.call(t, workerRuntime, "task/status", map[string]any{"key": fixture.task.ID})
	if message := tsk571ErrorMessage(t, workerLeadAction); !strings.Contains(message, "RUNTIME_SESSION_UNAVAILABLE") {
		t.Fatalf("Worker runtime acquired Lead authority: %q", message)
	}
	resolvedWorker, err := fixture.server.Service.ResolveWorkerSession(context.Background(), fixture.projectID, workerSession)
	if err != nil || resolvedWorker.Agent.AgentID != "coding-worker" {
		t.Fatalf("Worker Session resolved through unexpected Agent: session=%#v err=%v", resolvedWorker, err)
	}
}
