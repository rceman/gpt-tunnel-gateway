package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAgentResolverSelectsUsableRoleAndRecordsReasoningFallback(t *testing.T) {
	s, revision, _ := testService(t)
	resolved, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID:            "example",
		Role:                 model.AgentRoleCoding,
		RecommendedReasoning: model.ReasoningHigh,
		RequireUsable:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AgentID != "coder-example" || resolved.SessionKey != "example_master" || resolved.ResolvedReasoning != model.ReasoningHigh || resolved.Fallback {
		t.Fatalf("unexpected exact coding resolution: %#v", resolved)
	}
	maxBinding := config.AgentBinding{SessionKey: "example_max_master"}
	s.Config.AgentBindings[config.ProjectAgentBindingKey("example", "coder-max")] = maxBinding
	now := time.Now().UTC()
	_, operation, err := s.AgentRegister(trustedWorkflowPolicyContext(context.Background(), "planner"), AgentRegisterInput{
		Agent: model.Agent{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "coder-max", Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningMax, CreatedAt: now, UpdatedAt: now},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID:            "example",
		Role:                 model.AgentRoleCoding,
		RecommendedReasoning: model.ReasoningMax,
		RequireUsable:        true,
	})
	if err != nil || resolved.AgentID != "coder-max" || resolved.Fallback {
		t.Fatalf("exact max resolution failed: %#v err=%v", resolved, err)
	}
	_ = operation
	max := false
	_, operation, err = s.AgentUpdate(trustedWorkflowPolicyContext(context.Background(), "planner"), AgentUpdateInput{
		ProjectID: "example",
		AgentID:   "coder-max",
		Enabled:   &max,
		UpdatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID:            "example",
		Role:                 model.AgentRoleCoding,
		RecommendedReasoning: model.ReasoningMax,
		RequireUsable:        true,
	})
	if err != nil || resolved.AgentID != "coder-example" || !resolved.Fallback || resolved.FallbackReason != "preferred_reasoning_unavailable" {
		t.Fatalf("fallback resolution failed: %#v err=%v", resolved, err)
	}
}

func TestAgentResolverSeparatesWatcherBindingAndLegacyProjectSessionCalls(t *testing.T) {
	s, _, _ := testService(t)
	watcher, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID:     "example",
		Role:          model.AgentRoleWatcher,
		AgentID:       "watcher-example",
		RequireUsable: true,
	})
	if err != nil || watcher.AgentID != "watcher-example" || watcher.SessionKey != "watcher_master" {
		t.Fatalf("watcher resolution failed: %#v err=%v", watcher, err)
	}
	legacy, err := s.resolveAgentSession(context.Background(), "example")
	if err != nil || legacy != "example_master" {
		t.Fatalf("project agent session did not use coding registry binding: %q err=%v", legacy, err)
	}
}

func TestTaskDispatchPersistsResolvedAgentIdentity(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/resolved-agent")
	if run.AgentID != "coder-example" || run.SessionKey != "example_master" || run.ResolvedReasoning != model.ReasoningHigh || run.AgentFallback {
		t.Fatalf("dispatch did not persist resolved coding identity: %#v", run)
	}
	public, err := s.RunRead(context.Background(), run.ID)
	if err != nil || public.AgentID != run.AgentID {
		t.Fatalf("resolved agent identity was not durable: %#v err=%v", public, err)
	}
}
