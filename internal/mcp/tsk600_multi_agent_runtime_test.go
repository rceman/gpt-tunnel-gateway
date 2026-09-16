package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK629RuntimeKeyFailsClosedDespiteAmbiguousBindings(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK571Agent(t, fixture.server.Service, revision, "coding-secondary", true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-secondary"] = config.AgentBinding{SessionKey: fixture.runtime}

	result := fixture.call(t, fixture.runtime, "agent/status", map[string]any{})
	message := tsk571ErrorMessage(t, result)
	if !strings.Contains(message, "durable Session") {
		t.Fatalf("ambiguous runtime-key caller was not rejected at the durable Session boundary: %q", message)
	}
}

func TestTSK629LogicalAgentAndTaskRoutingStaySeparate(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	workerRuntime := "runtime-tsk600-worker"
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK571Agent(t, fixture.server.Service, revision, "coding-worker", true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-worker"] = config.AgentBinding{SessionKey: workerRuntime}
	workerSession := fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, workerRuntime)
	planner := fixture.sessions[durableSession.RolePlanner]

	for _, agent := range []string{fixture.agentID, "coding-worker"} {
		status := fixture.call(t, planner, "agent/status", map[string]any{"agent": agent})
		if status["ok"] != true {
			t.Fatalf("Planner could not address logical Agent %s: %#v", agent, status)
		}
	}
	dispatched := fixture.call(t, planner, "task/dispatch", map[string]any{"key": fixture.task.ID})
	if dispatched["ok"] != true {
		t.Fatalf("Task dispatch failed with distinct Lead and Worker Agents: %#v", dispatched)
	}
	dispatchResult, ok := dispatched["result"].(map[string]any)
	if !ok || dispatchResult["agent"] != "coding-worker" {
		t.Fatalf("Task dispatch did not derive the attached Worker: %#v", dispatched)
	}
	workerRead := fixture.call(t, workerSession, "task/read", map[string]any{"key": fixture.task.ID})
	if workerRead["ok"] != true {
		t.Fatalf("Worker Session lost Task authority: %#v", workerRead)
	}
	resolvedWorker, err := fixture.server.Service.ResolveWorkerSession(context.Background(), fixture.projectID, workerSession)
	if err != nil || resolvedWorker.Agent.AgentID != "coding-worker" {
		t.Fatalf("Worker Session resolved through unexpected Agent: session=%#v err=%v", resolvedWorker, err)
	}
}
