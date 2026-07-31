package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestPlanCutoverMCPCurrentFixtureStrictAndOneTime(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
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
	srv := &Server{Service: s}
	before := installed.After
	read := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"plan_read","arguments":{"project_id":"example"}}}`))
	readResult := read["result"].(map[string]any)
	if readResult["isError"] != true {
		t.Fatalf("schema-v1 read unexpectedly succeeded: %#v", read)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || after != before {
		t.Fatalf("ordinary plan_read mutated hub: before=%s after=%s err=%v", before, after, err)
	}
	unknown := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"plan_cutover","arguments":{"project_id":"example","updated_by":"owner","unknown":true}}}`))
	if _, ok := unknown["error"].(map[string]any); !ok {
		t.Fatalf("unknown cutover field was accepted: %#v", unknown)
	}
	success := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"plan_cutover","arguments":{"project_id":"example","updated_by":"owner"}}}`))
	successResult := success["result"].(map[string]any)
	if successResult["isError"] != false {
		t.Fatalf("cutover failed: %#v", success)
	}
	content := successResult["structuredContent"].(map[string]any)
	if content["status"] != "cut over" {
		t.Fatalf("unexpected cutover content: %#v", content)
	}
	repeat := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"plan_cutover","arguments":{"project_id":"example","updated_by":"owner"}}}`))
	if repeat["result"].(map[string]any)["isError"] != true {
		t.Fatalf("second cutover was accepted: %#v", repeat)
	}
}

func TestMCPReadOnlyBootstrapErrorIsBounded(t *testing.T) {
	state := t.TempDir()
	srv := &Server{Service: service.New(config.Config{StateDir: state})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"project_list","arguments":{}}}`))
	result := response["result"].(map[string]any)
	data, _ := json.Marshal(result)
	if result["isError"] != true || strings.Contains(string(data), state) || strings.Contains(string(data), "hub/repository") {
		t.Fatalf("unbounded MCP read error: %s", data)
	}
}

func TestSectionalPlanMCPToolsExposeCompactReadAndExplicitFullOperations(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: "airelay", Controller: config.ControllerConfig{TunnelHealthListenAddr: "127.0.0.1:8876"}, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
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
	srv := &Server{Service: s}
	read := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"plan_read","arguments":{"project_id":"example"}}}`))
	readResult := read["result"].(map[string]any)
	manifest := readResult["structuredContent"].(map[string]any)
	if manifest["schema_version"] != float64(model.PlanSchemaVersion) {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if _, ok := manifest["description"]; ok {
		t.Fatalf("compact manifest exposed a description: %#v", manifest)
	}
	createBody := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"plan_section_create","arguments":{"project_id":"example","section_id":"operations","title":"Operations","short_description":"Operational steps","description":"Full operational description","updated_by":"gpt","expected_hub_revision":"` + planOp.Hub.After + `"}}}`)
	created := callMCP(t, srv, createBody)["result"].(map[string]any)
	if created["isError"] != false {
		t.Fatalf("section create failed: %#v", created)
	}
	sectionRead := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"plan_section_read","arguments":{"project_id":"example","section_id":"operations"}}}`))["result"].(map[string]any)
	section := sectionRead["structuredContent"].(map[string]any)
	if section["description"] != "Full operational description" {
		t.Fatalf("section read did not return full description: %#v", section)
	}
	updated := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"plan_section_update","arguments":{"project_id":"example","section_id":"operations","description":"Updated description","updated_by":"gpt","expected_section_revision":1}}}`))["result"].(map[string]any)
	if updated["isError"] != false {
		t.Fatalf("section update failed: %#v", updated)
	}
	rendered := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"plan_render","arguments":{"project_id":"example"}}}`))["result"].(map[string]any)
	render := rendered["structuredContent"].(map[string]any)
	if !strings.Contains(render["text"].(string), "Updated description") {
		t.Fatalf("render omitted section description: %#v", render)
	}
	status := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"project_status","arguments":{"project_id":"example"}}}`))["result"].(map[string]any)
	statusContent, err := json.Marshal(status["structuredContent"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(statusContent), "Updated description") {
		t.Fatalf("project status exposed full section description: %s", statusContent)
	}
}
