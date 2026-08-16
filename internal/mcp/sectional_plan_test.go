package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func planCall(t *testing.T, sessionID, action string, input map[string]any) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": action, "input": input}}})
}

func TestRetiredPlanActionsAreAbsentFromCanonicalRegistry(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	entries := server.genericActionRegistry(server.tools())
	for _, action := range []string{"plan/read", "plan/cutover", "plan/update", "plan/section_read", "plan/render", "plan/history"} {
		if _, ok := entries[action]; ok {
			t.Fatalf("retired plan action %q remains registered", action)
		}
	}
}

func TestMCPReadOnlyBootstrapErrorIsBounded(t *testing.T) {
	state := t.TempDir()
	srv := &Server{Service: service.New(config.Config{StateDir: state})}
	response := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "bootstrap", "arguments": map[string]any{}}}))
	structured := genericStructured(t, response)
	data, _ := json.Marshal(structured)
	if strings.Contains(string(data), state) || strings.Contains(string(data), "hub/repository") {
		t.Fatalf("unbounded MCP read error: %s", data)
	}
}

func TestProjectStatusDoesNotReadCurrentPlan(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: "airelay", Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"}, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	title, summary := "Plan", "Summary"
	_, err = s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, UpdatedBy: "gpt", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Plan.Revision != 0 || len(status.Plan.Queue) != 0 || len(status.Plan.Sections) != 0 {
		t.Fatalf("project status retained current Plan authority: %#v", status.Plan)
	}
	if _, err := s.PlanRead(context.Background(), "example"); err != nil {
		t.Fatalf("historical Plan read was lost: %v", err)
	}
}
