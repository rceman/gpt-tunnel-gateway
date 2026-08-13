package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestProjectIdentifiersMCPContractsAndHappyPath(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tools := server.tools()
	readTool, ok := tools["project_identifiers_read"]
	if !ok {
		t.Fatal("project_identifiers_read is not registered internally")
	}
	adoptTool, ok := tools["project_identifiers_adopt"]
	if !ok {
		t.Fatal("project_identifiers_adopt is not registered internally")
	}
	if readTool.Annotations != readOnlyAnnotations() || adoptTool.Annotations != additiveExternalAnnotations() {
		t.Fatalf("unexpected identifier annotations: read=%+v adopt=%+v", readTool.Annotations, adoptTool.Annotations)
	}
	if readTool.InputSchema["additionalProperties"] != false || adoptTool.InputSchema["additionalProperties"] != false {
		t.Fatal("identifier input schemas must be closed")
	}
	if err := validateOutputValue(readTool.OutputSchema, normalizeObject(model.ProjectIdentifiers{SchemaVersion: 1, ProjectID: "example", ProjectCode: "GTW", NextTaskNumber: 1, NextADRNumber: 1})); err != nil {
		t.Fatalf("identifier output schema rejected a valid record: %v", err)
	}
	for _, invalid := range []model.ProjectIdentifiers{
		{SchemaVersion: 2, ProjectID: "example", ProjectCode: "GTW", NextTaskNumber: 1, NextADRNumber: 1},
		{SchemaVersion: 1, ProjectID: "Bad", ProjectCode: "GTW", NextTaskNumber: 1, NextADRNumber: 1},
		{SchemaVersion: 1, ProjectID: "example", ProjectCode: "gtw", NextTaskNumber: 1, NextADRNumber: 1},
		{SchemaVersion: 1, ProjectID: "example", ProjectCode: "GTW", NextTaskNumber: 0, NextADRNumber: 1},
		{SchemaVersion: 1, ProjectID: "example", ProjectCode: "GTW", NextTaskNumber: 1, NextADRNumber: model.MaxSafeInteger + 1},
	} {
		if err := validateOutputValue(readTool.OutputSchema, normalizeObject(invalid)); err == nil {
			t.Fatalf("invalid identifier output was accepted: %#v", invalid)
		}
	}
	adoptProperties := adoptTool.InputSchema["properties"].(map[string]any)
	projectCode := adoptProperties["project_code"].(map[string]any)
	if projectCode["pattern"] != "^[A-Z]{3}$" {
		t.Fatalf("project code pattern=%v", projectCode["pattern"])
	}
	if _, ok := server.publicTools()["project_identifiers_adopt"]; ok {
		t.Fatal("obsolete identifier-adopt tool is publicly registered")
	}

	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	_, secondRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{
		"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
		"second":  {Root: secondRoot, Mirror: filepath.Join(dir, "second-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "second_master"},
	}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	currentHub := registered.Hub.After
	secondProject := model.Project{SchemaVersion: 1, ID: "second", RepositoryURL: "git@example.invalid:second.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("b", 40), Status: "active"}
	if registered, err = s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: secondProject, WriteOptions: service.WriteOptions{ExpectedHubRevision: currentHub}}); err != nil {
		t.Fatal(err)
	}
	server = &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	exampleSession := genericSession(t, s, "example")
	secondSession := genericSession(t, s, "second")
	request := func(id int, session, action string, input map[string]any) []byte {
		return mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": session, "action": action, "input": input}}})
	}

	adopted := genericActionResult(t, callMCP(t, server, request(1, exampleSession, "project/identifiers_adopt", map[string]any{"project_id": "example", "project_code": "GTW"})))
	if adopted["identifiers"].(map[string]any)["project_code"] != "GTW" {
		t.Fatalf("unexpected adoption result: %#v", adopted)
	}
	read := genericActionResult(t, callMCP(t, server, request(2, exampleSession, "project/identifiers_read", map[string]any{"project_id": "example"})))
	if read["project_code"] != "GTW" {
		t.Fatalf("unexpected identifier read result: %#v", read)
	}
	duplicate := genericStructured(t, callMCP(t, server, request(3, exampleSession, "project/identifiers_adopt", map[string]any{"project_id": "example", "project_code": "GTW"})))
	if duplicate["is_error"] != true {
		t.Fatalf("duplicate adoption was accepted: %#v", duplicate)
	}
	duplicateCode := genericStructured(t, callMCP(t, server, request(6, secondSession, "project/identifiers_adopt", map[string]any{"project_id": "second", "project_code": "GTW"})))
	if duplicateCode["is_error"] != true {
		t.Fatalf("duplicate project code was accepted: %#v", duplicateCode)
	}
	unknown := callMCP(t, server, request(4, exampleSession, "project/identifiers_read", map[string]any{"project_id": "example", "unknown": true}))
	if result := genericStructured(t, unknown); result["is_error"] != true {
		t.Fatalf("unknown identifier field was accepted: %#v", unknown)
	}
	malformed := genericStructured(t, callMCP(t, server, request(5, exampleSession, "project/identifiers_adopt", map[string]any{"project_id": "example", "project_code": "gtw"})))
	if malformed["is_error"] != true {
		t.Fatalf("malformed project code was accepted: %#v", malformed)
	}
}
