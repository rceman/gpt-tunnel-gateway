package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestProjectOnboardCLIExposesDurablePlannerToken(t *testing.T) {
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{
		BeforeStart: func(gateway *testutil.LiveGateway) {
			airelay := filepath.Join(gateway.BaseDir, "airelay")
			script := "#!/bin/sh\ncase \"$1\" in\nsession-status) printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\" ;;\n*) exit 99 ;;\nesac\n"
			if err := os.WriteFile(airelay, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			gateway.Config.AirelayCommand = airelay
			gateway.WriteConfig(t)
			if err := (hub.Store{Config: gateway.Config}).Ensure(context.Background()); err != nil {
				t.Fatal(err)
			}
			seedProjectOnboardHubRule(t, gateway)
		},
	})
	projectRemote := strings.TrimSpace(testutil.Git(t, gateway.ProjectRoot, "remote", "get-url", "origin"))
	if projectRemote == gateway.HubRemote {
		t.Fatal("live gateway Hub and project repositories share one remote")
	}
	firstResult := gateway.MustCLI(t, testutil.LiveCommandOptions{}, "project", "onboard", "--root", gateway.ProjectRoot, "AIR", "agentir_worker")
	var first map[string]any
	if err := json.Unmarshal([]byte(firstResult.Stdout), &first); err != nil {
		t.Fatal(err)
	}
	if first["status"] != "onboarded" || first["token"] == "" || first["token_usage"] != service.ProjectOnboardTokenUsage {
		t.Fatal("fresh onboarding did not return the expected token contract")
	}
	token := first["token"].(string)
	if agents, ok := first["agents"].([]any); !ok || len(agents) != 1 || agents[0].(map[string]any)["agent"] != "AIR-WORKER" {
		t.Fatalf("onboard agents=%#v", first["agents"])
	}
	projectID := filepath.Base(gateway.ProjectRoot)
	persisted, err := config.Load(gateway.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := persisted.ProjectAgentBindings[projectID]["AIR-WORKER"]
	if !ok || binding.SessionKey != "agentir_worker" {
		t.Fatalf("onboard Agent binding was not persisted through daemon host config %q: %#v", gateway.ConfigPath, persisted.ProjectAgentBindings)
	}
	retryResult := gateway.MustCLI(t, testutil.LiveCommandOptions{}, "project", "onboard", "--root", gateway.ProjectRoot, "AIR", "agentir_worker")
	var retry map[string]any
	if err := json.Unmarshal([]byte(retryResult.Stdout), &retry); err != nil {
		t.Fatal(err)
	}
	if retry["status"] != "already_registered" || retry["token"] != token || retry["token_usage"] != service.ProjectOnboardTokenUsage {
		t.Fatal("repeat onboarding did not return the stable token contract")
	}
	conflict := gateway.RunCLI(testutil.LiveCommandOptions{}, "project", "onboard", "--root", gateway.ProjectRoot, "BAD", "agentir_worker")
	if conflict.Err == nil || !strings.Contains(conflict.Stderr, "conflicts with repository identity or project code") || strings.Contains(conflict.Stderr, "the requested operator operation failed") {
		t.Fatalf("project onboard did not surface the daemon-side conflict: stderr=%q err=%v", conflict.Stderr, conflict.Err)
	}
	started, err := gateway.MCPCall(context.Background(), "session_start", map[string]any{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := started["session"].(string)
	if sessionID == "" || started["role"] != "planner" {
		t.Fatalf("session_start output=%#v", started)
	}
	mcpSurfaces := []any{started}
	for _, action := range []string{"project/status", "session/list", "session/info", "agent/guide", "runtime/logs"} {
		value, err := gateway.MCPCall(context.Background(), "call", map[string]any{"session": sessionID, "action": action, "input": map[string]any{}})
		if err != nil {
			t.Fatalf("MCP %s: %v", action, err)
		}
		mcpSurfaces = append(mcpSurfaces, value)
	}
	projects, err := gateway.MCPCall(context.Background(), "projects", map[string]any{"gateway": "HOM"})
	if err != nil {
		t.Fatal(err)
	}
	guide, err := gateway.MCPCall(context.Background(), "guide", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(append(mcpSurfaces, projects, guide))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("project bootstrap token leaked through public MCP projections")
	}
}

func seedProjectOnboardHubRule(t *testing.T, gateway *testutil.LiveGateway) {
	t.Helper()
	ctx := context.Background()
	projectID := filepath.Base(gateway.ProjectRoot)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	rule := model.Rule{
		SchemaVersion: model.SchemaVersion, ID: "AIR-RUL9", ProjectID: projectID, Revision: 1,
		Title: "Existing portable Rule", Status: model.RuleStatusProposed, Name: "onboarding.fixture", Value: json.RawMessage(`true`),
		CreatedBy: "planner", CreatedAt: now, UpdatedBy: "planner", UpdatedAt: now,
	}
	payload, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	portable := struct {
		EntityType    string          `json:"entity_type"`
		EntityID      string          `json:"entity_id"`
		ProjectID     string          `json:"project_id"`
		Revision      int64           `json:"revision"`
		MutationKind  string          `json:"mutation_kind"`
		Actor         string          `json:"actor"`
		Reason        string          `json:"reason"`
		ChangedFields []string        `json:"changed_fields"`
		Payload       json.RawMessage `json:"payload"`
		RecordedAt    string          `json:"recorded_at"`
	}{
		EntityType:    "rule",
		EntityID:      rule.ID,
		ProjectID:     projectID,
		Revision:      1,
		MutationKind:  "create",
		Actor:         "planner",
		Reason:        "created",
		ChangedFields: []string{"title", "summary", "name", "value", "description"},
		Payload:       payload,
		RecordedAt:    now.Format(time.RFC3339Nano),
	}
	prefix := "projects/" + projectID
	paths := []string{prefix + "/rules/" + rule.ID + ".json", prefix + "/entity-revisions/rule/" + rule.ID + "/REV1.json"}
	if _, err := (hub.Store{Config: gateway.Config}).Transact(ctx, "", "seed existing portable Rule", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, paths[0], rule); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, paths[1], portable); err != nil {
			return nil, err
		}
		return paths, nil
	}); err != nil {
		t.Fatal(err)
	}
}
