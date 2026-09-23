package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK590LeadBindingBindRebindAndGatewayRestart(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, false, true)
	installTSK563Airelay(t, fixture)
	ctx := context.Background()

	if _, err := fixture.server.Service.ResolveProjectLead(ctx, fixture.projectID); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
		t.Fatalf("unbound Lead was not rejected: %v", err)
	}
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID] = map[string]config.AgentBinding{
		fixture.agentID: {SessionKey: fixture.runtime},
	}
	initial, err := fixture.server.Service.ResolveProjectLead(ctx, fixture.projectID)
	if err != nil {
		t.Fatalf("bound Lead did not resolve: %v", err)
	}
	if initial.Agent.AgentID != fixture.agentID || initial.Session.ID != fixture.sessions[durableSession.RoleLead] || initial.Session.Role != durableSession.RoleLead || initial.Binding.SessionKey != fixture.runtime {
		t.Fatalf("unexpected bound Lead identity: %#v", initial)
	}

	restarted := service.NewWithDurabilityDeferredWorkers(fixture.server.Service.Config, fixture.server.Service.Durability)
	restarted.Airelay.Timeout = time.Second
	afterRestart, err := restarted.ResolveProjectLead(ctx, fixture.projectID)
	if err != nil {
		t.Fatalf("Gateway restart lost Lead binding: %v", err)
	}
	if afterRestart.Agent.AgentID != initial.Agent.AgentID || afterRestart.Session.ID != initial.Session.ID || afterRestart.Binding.SessionKey != initial.Binding.SessionKey {
		t.Fatalf("Gateway restart changed Lead identity: before=%#v after=%#v", initial, afterRestart)
	}

	reboundRuntime := "runtime-tsk590-lead-rebound"
	reboundSession := fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleLead, reboundRuntime)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID][fixture.agentID] = config.AgentBinding{SessionKey: reboundRuntime}
	rebound, err := fixture.server.Service.ResolveProjectLead(ctx, fixture.projectID)
	if err != nil {
		t.Fatalf("rebound Lead did not resolve: %v", err)
	}
	if rebound.Session.ID != reboundSession || rebound.Binding.SessionKey != reboundRuntime || rebound.Agent.AgentID != initial.Agent.AgentID {
		t.Fatalf("Lead rebind selected the wrong identity: %#v", rebound)
	}
	if _, err := fixture.server.Service.ResolveRuntimeRoleSession(ctx, fixture.runtime, durableSession.RoleLead); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
		t.Fatalf("stale Lead runtime remained authorized after rebind: %v", err)
	}

	restarted.Config.ProjectAgentBindings = fixture.server.Service.Config.ProjectAgentBindings
	afterRebindRestart, err := restarted.ResolveProjectLead(ctx, fixture.projectID)
	if err != nil {
		t.Fatalf("Gateway restart lost rebound Lead binding: %v", err)
	}
	if afterRebindRestart.Session.ID != rebound.Session.ID || afterRebindRestart.Binding.SessionKey != rebound.Binding.SessionKey {
		t.Fatalf("rebound Lead changed after restart: before=%#v after=%#v", rebound, afterRebindRestart)
	}
}

func TestTSK590LeadBindingRejectsWrongProjectAndAmbiguity(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	ctx := context.Background()

	t.Run("wrong project", func(t *testing.T) {
		fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
		installTSK563Airelay(t, fixture)
		_, err := fixture.server.Service.ResolveLeadSession(ctx, "other", fixture.sessions[durableSession.RoleLead])
		if err == nil || !strings.Contains(err.Error(), "RUNTIME_SESSION_UNAVAILABLE") {
			t.Fatalf("wrong-project Lead Session was not rejected: %v", err)
		}
		if _, err := fixture.server.Service.ResolveProjectLead(ctx, "other"); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
			t.Fatalf("wrong-project Lead binding was not rejected: %v", err)
		}
	})

	t.Run("duplicate Lead Sessions", func(t *testing.T) {
		fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
		installTSK563Airelay(t, fixture)
		fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleLead, fixture.runtime)
		_, err := fixture.server.Service.ResolveProjectLead(ctx, fixture.projectID)
		if err == nil || !strings.Contains(err.Error(), "RUNTIME_SESSION_AMBIGUOUS") {
			t.Fatalf("duplicate Lead binding did not fail closed: %v", err)
		}
	})

	t.Run("multiple Lead Agents", func(t *testing.T) {
		fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
		installTSK563Airelay(t, fixture)
		revision, err := fixture.server.Service.Hub.RemoteRevision(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seedTSK571Agent(t, fixture.server.Service, revision, "coding-secondary", true)
		secondaryRuntime := "runtime-tsk590-secondary"
		fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-secondary"] = config.AgentBinding{SessionKey: secondaryRuntime}
		fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleLead, secondaryRuntime)
		_, err = fixture.server.Service.ResolveProjectLead(ctx, fixture.projectID)
		if err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_AMBIGUOUS") {
			t.Fatalf("multiple Lead Agents did not fail closed: %v", err)
		}
	})
}

func TestTSK590LeadAndWorkerRoleAuthorityIsolatedOverHTTP(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	ctx := context.Background()
	workerRuntime := "runtime-tsk590-worker"
	revision, err := fixture.server.Service.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seedTSK571Agent(t, fixture.server.Service, revision, "coding-worker", true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-worker"] = config.AgentBinding{SessionKey: workerRuntime}
	workerSession := fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, workerRuntime)

	leadResolved, err := fixture.server.Service.ResolveProjectLead(ctx, fixture.projectID)
	if err != nil {
		t.Fatalf("Lead binding did not resolve: %v", err)
	}
	workerResolved, err := fixture.server.Service.ResolveProjectWorker(ctx, fixture.projectID)
	if err != nil {
		t.Fatalf("Worker binding did not resolve: %v", err)
	}
	if leadResolved.Session.Role != durableSession.RoleLead || workerResolved.Session.Role != durableSession.RoleWorker || leadResolved.Session.ID == workerResolved.Session.ID || leadResolved.Agent.AgentID == workerResolved.Agent.AgentID {
		t.Fatalf("Lead and Worker authorities were merged: lead=%#v worker=%#v", leadResolved, workerResolved)
	}

	leadStatus := fixture.call(t, fixture.sessions[durableSession.RoleLead], "task/status", map[string]any{"key": fixture.task.ID})
	if leadStatus["ok"] != true {
		t.Fatalf("durable Lead task/status failed: %#v", leadStatus)
	}
	workerRead := fixture.call(t, workerSession, "task/read", map[string]any{"key": fixture.task.ID})
	if workerRead["ok"] != true {
		t.Fatalf("durable Worker task/read failed: %#v", workerRead)
	}
	if workerResolved.Session.ID != workerSession {
		t.Fatalf("Worker resolver selected unexpected Session: %#v", workerResolved)
	}
}

func TestTSK590PlannerDelegatesTrackThroughDurableMessage(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	ctx := context.Background()
	milestone, _, err := fixture.server.Service.MilestoneLifecycleCreate(ctx, service.MilestoneCreateInput{
		ProjectID: fixture.projectID, Title: "TSK590 delegation milestone", Tasks: []string{fixture.task.ID}, CreatedBy: "planner",
	}, "tsk590-delegation-milestone")
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := fixture.server.Service.TrackLifecycleCreate(ctx, service.TrackCreateInput{
		ProjectID: fixture.projectID, Milestone: milestone.ID, Title: "TSK590 delegation Track", Tasks: []string{fixture.task.ID}, CreatedBy: "planner",
	}, "tsk590-delegation-track")
	if err != nil {
		t.Fatal(err)
	}
	planner := fixture.sessions[durableSession.RolePlanner]
	for _, action := range []struct {
		name  string
		input map[string]any
	}{
		{name: "agent/status", input: map[string]any{"agent": fixture.agentID}},
		{name: "agent/await", input: map[string]any{"agent": fixture.agentID, "seconds": 1}},
		{name: "agent/tail", input: map[string]any{"agent": fixture.agentID, "lines": 1}},
	} {
		result := fixture.call(t, planner, action.name, action.input)
		if result["ok"] != true {
			t.Fatalf("Planner could not use generic %s for observation: %#v", action.name, result)
		}
	}
	delegation := fixture.call(t, planner, "message/create", map[string]any{"to_role": durableSession.RoleLead, "body": track.ID})
	if delegation["ok"] != true {
		t.Fatalf("Planner could not durably delegate the Track: %#v", delegation)
	}
	created, ok := delegation["result"].(map[string]any)
	if !ok {
		t.Fatalf("message/create result=%#v", delegation)
	}
	messageID, _ := created["message"].(string)
	read := fixture.call(t, fixture.sessions[durableSession.RoleLead], "message/read", map[string]any{"message": messageID})
	if read["ok"] != true {
		t.Fatalf("Lead could not read durable Track delegation: %#v", read)
	}
	message, _ := read["result"].(map[string]any)
	if message["body"] != track.ID {
		t.Fatalf("durable delegation body=%#v", read)
	}
}
