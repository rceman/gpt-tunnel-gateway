package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func planCall(t *testing.T, sessionID, action string, input map[string]any) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": action, "input": input}}})
}

func TestPlanCutoverMCPCurrentFixtureStrictAndOneTime(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "plan_v1_current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(fixture, &legacy); err != nil {
		t.Fatal(err)
	}
	installed, err := s.Hub.Transact(context.Background(), registered.Hub.After, "test: install current plan fixture", func(w string) ([]string, error) {
		path := hub.ProtocolRoot + "/projects/example/plan/current.json"
		if err := hub.WriteJSON(w, path, legacy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, s, "example")
	before := installed.After
	read := genericStructured(t, callMCP(t, srv, planCall(t, sessionID, "plan/read", map[string]any{})))
	if read["is_error"] != true {
		t.Fatalf("schema-v1 read unexpectedly succeeded: %#v", read)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || after != before {
		t.Fatalf("ordinary plan/read mutated hub: before=%s after=%s err=%v", before, after, err)
	}
	unknown := genericStructured(t, callMCP(t, srv, planCall(t, sessionID, "plan/cutover", map[string]any{"updated_by": "owner", "unknown": true})))
	if unknown["is_error"] != true {
		t.Fatalf("unknown cutover field was accepted: %#v", unknown)
	}
	success := genericActionResult(t, callMCP(t, srv, planCall(t, sessionID, "plan/cutover", map[string]any{"updated_by": "owner"})))
	if success["status"] != "cut over" {
		t.Fatalf("unexpected cutover content: %#v", success)
	}
	repeat := genericStructured(t, callMCP(t, srv, planCall(t, sessionID, "plan/cutover", map[string]any{"updated_by": "owner"})))
	if repeat["is_error"] != true {
		t.Fatalf("second cutover was accepted: %#v", repeat)
	}
}

func TestMCPReadOnlyBootstrapErrorIsBounded(t *testing.T) {
	state := t.TempDir()
	srv := &Server{Service: service.New(config.Config{StateDir: state})}
	response := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "project", "arguments": map[string]any{"action": "list", "input": map[string]any{}}}}))
	structured := genericStructured(t, response)
	data, _ := json.Marshal(structured)
	if structured["is_error"] != true || strings.Contains(string(data), state) || strings.Contains(string(data), "hub/repository") {
		t.Fatalf("unbounded MCP read error: %s", data)
	}
}

func TestSectionalPlanMCPToolsExposeCompactReadAndExplicitFullOperations(t *testing.T) {
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
	planOp, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, UpdatedBy: "gpt", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	sessionID := genericSession(t, s, "example")
	read := genericActionResult(t, callMCP(t, srv, planCall(t, sessionID, "plan/read", map[string]any{})))
	if read["schema_version"] != float64(model.PlanSchemaVersion) || read["description"] != nil {
		t.Fatalf("unexpected compact manifest: %#v", read)
	}
	created := genericActionResult(t, callMCP(t, srv, planCall(t, sessionID, "plan/section_create", map[string]any{"section_id": "operations", "title": "Operations", "short_description": "Operational steps", "description": "Full operational description", "updated_by": "gpt", "expected_hub_revision": planOp.Hub.After})))
	if created["status"] == nil {
		t.Fatalf("section create failed: %#v", created)
	}
	section := genericActionResult(t, callMCP(t, srv, planCall(t, sessionID, "plan/section_read", map[string]any{"section_id": "operations"})))
	if section["description"] != "Full operational description" {
		t.Fatalf("section read did not return full description: %#v", section)
	}
	updated := genericActionResult(t, callMCP(t, srv, planCall(t, sessionID, "plan/section_update", map[string]any{"section_id": "operations", "description": "Updated description", "updated_by": "gpt", "expected_section_revision": 1})))
	if updated["status"] == nil {
		t.Fatalf("section update failed: %#v", updated)
	}
	rendered := genericActionResult(t, callMCP(t, srv, planCall(t, sessionID, "plan/render", map[string]any{})))
	if !strings.Contains(rendered["text"].(string), "Updated description") {
		t.Fatalf("render omitted section description: %#v", rendered)
	}
	statusEnvelope := genericStructured(t, callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{"name": "status", "arguments": map[string]any{"session_id": sessionID}}})))
	status, ok := statusEnvelope["project_status"].(map[string]any)
	if !ok {
		t.Fatalf("status omitted project status: %#v", statusEnvelope)
	}
	statusContent, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(statusContent), "Updated description") {
		t.Fatalf("project status exposed full section description: %s", statusContent)
	}
}
