package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestCanonicalAgentAwaitUsesLocalAuthorityWhenHubUnavailableAndLocked(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	airelay := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nprintf 'Controller: reachable\\nState: idle\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		SchemaVersion:          1,
		GatewayID:              "test-gateway",
		StateDir:               stateDir,
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           2000,
		DispatchTimeoutSeconds: 1,
		AirelayCommand:         airelay,
		Hub: config.HubConfig{
			RepositoryURL: filepath.Join(t.TempDir(), "unavailable-hub.git"),
			Branch:        "main",
		},
		Projects: map[string]config.ProjectConfig{
			"example": {
				Root:              filepath.Join(t.TempDir(), "project"),
				Mirror:            filepath.Join(t.TempDir(), "mirror.git"),
				Remote:            "origin",
				DefaultBranch:     "main",
				ProjectCode:       "EXM",
				AirelaySessionKey: "example_master",
			},
		},
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	configuration := model.DefaultProjectConfiguration("example", now)
	configurationPayload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{
		ID:        configuration.ProjectID,
		Revision:  int64(configuration.Revision),
		Payload:   configurationPayload,
		UpdatedAt: configuration.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	agent := model.Agent{
		SchemaVersion:        model.AgentSchemaVersion,
		ProjectID:            "example",
		AgentID:              "coding-example",
		Role:                 model.AgentRoleCoding,
		Enabled:              true,
		RecommendedReasoning: model.ReasoningHigh,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	agentPayload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceLocalAgents(ctx, "example", []sqlitestore.LocalAgent{{
		ProjectID: "example",
		AgentID:   agent.AgentID,
		Payload:   agentPayload,
		UpdatedAt: now.Format(time.RFC3339Nano),
	}}); err != nil {
		t.Fatal(err)
	}

	s := service.NewWithDurabilityDeferredWorkers(c, db)
	session, err := durableSession.NewStore(stateDir).Create(durableSession.CreateInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		Role:        durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	hubLock, err := lockfile.Acquire(filepath.Join(stateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal(err)
	}
	defer hubLock.Release()

	bound := service.WithAgentSessionID(ctx, session.ID)
	server := &Server{Service: s}
	agents, err := s.AgentList(bound, "example")
	if err != nil || len(agents) != 1 || agents[0].AgentID != agent.AgentID {
		t.Fatalf("local Agent list failed with Hub unavailable/locked: agents=%#v err=%v", agents, err)
	}
	// The fixture is seeded before making the list policy unusable. Every
	// operation below has an explicit selector; a hidden AgentList call must
	// therefore fail rather than silently passing through the registry.
	s.Config.MaxListItems = 0
	status, err := s.AgentRegistryStatus(bound, "example", agent.AgentID)
	if err != nil || !status.Usable || status.State != "usable" {
		t.Fatalf("local Agent status failed with Hub unavailable/locked: status=%#v err=%v", status, err)
	}
	canonicalStatus, err := server.canonicalAgentStatusAction(bound, mustJSON(t, map[string]any{
		"agent": agent.AgentID,
	}))
	if err != nil {
		t.Fatalf("canonical agent/status failed with Hub unavailable/locked: %v", err)
	}
	statusResult, ok := canonicalStatus.(map[string]any)
	if !ok || statusResult["agent"] != agent.AgentID {
		t.Fatalf("unexpected local agent/status result: %#v", canonicalStatus)
	}
	canonicalTail, err := server.canonicalAgentTailAction(bound, mustJSON(t, map[string]any{
		"agent": agent.AgentID,
		"lines": 30,
	}))
	if err != nil {
		t.Fatalf("canonical agent/tail failed with Hub unavailable/locked: %v", err)
	}
	tailResult, ok := canonicalTail.(map[string]any)
	if !ok || tailResult["agent"] != agent.AgentID {
		t.Fatalf("unexpected local agent/tail result: %#v", canonicalTail)
	}
	entries := server.genericActionRegistry(server.tools())
	promptTool, ok := entries["agent/prompt"]
	if !ok {
		t.Fatal("canonical agent/prompt tool is not registered")
	}
	promptValue, err := promptTool.Execute(bound, mustJSON(t, map[string]any{
		"agent":   agent.AgentID,
		"message": "TSK460 explicit selector",
	}))
	if err != nil {
		t.Fatalf("canonical agent/prompt failed to enqueue with Hub unavailable/locked: %v", err)
	}
	promptResult, ok := promptValue.(map[string]any)
	if !ok || promptResult["operation"] == "" || promptResult["status"] != "accepted" {
		t.Fatalf("unexpected local agent/prompt result: %#v", promptValue)
	}
	operationID, ok := promptResult["operation"].(string)
	if !ok {
		t.Fatalf("agent/prompt returned invalid operation identity: %#v", promptValue)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		value, statusErr := s.AgentIPCOperationStatus(bound, operationID, "agent-prompt")
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		receipt, receiptOK := value.(service.AgentPromptReceipt)
		if receiptOK && (receipt.Status == "completed" || receipt.Status == "failed") {
			if receipt.Status != "completed" {
				t.Fatalf("agent/prompt operation failed with Hub unavailable/locked: %#v", receipt)
			}
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("agent/prompt operation did not reach a terminal receipt: %#v", value)
		}
		time.Sleep(10 * time.Millisecond)
	}

	started := time.Now()
	result, err := server.canonicalAgentAwaitAction(bound, mustJSON(t, map[string]any{
		"agent":   agent.AgentID,
		"seconds": 1,
	}))
	if err != nil {
		t.Fatalf("canonical agent/await failed with Hub unavailable/locked: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 1500*time.Millisecond {
		t.Fatalf("canonical agent/await exceeded its bounded fast wait: %s", elapsed)
	}
	awaited, ok := result.(map[string]any)
	if !ok || awaited["agent"] != agent.AgentID || awaited["status"] != "idle" {
		t.Fatalf("unexpected local agent/await result: %#v", result)
	}
}
