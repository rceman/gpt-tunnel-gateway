package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
		t.Fatal("project_identifiers_read is not registered")
	}
	adoptTool, ok := tools["project_identifiers_adopt"]
	if !ok {
		t.Fatal("project_identifiers_adopt is not registered")
	}
	if readTool.Annotations != readOnlyAnnotations() {
		t.Fatalf("read annotations=%+v", readTool.Annotations)
	}
	if adoptTool.Annotations != additiveExternalAnnotations() {
		t.Fatalf("adopt annotations=%+v", adoptTool.Annotations)
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

	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	_, secondRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{
		"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
		"second":  {Root: secondRoot, Mirror: filepath.Join(dir, "second-mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "second_master"},
	}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	if _, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}}); err != nil {
		t.Fatal(err)
	}
	server = &Server{Service: s}
	request := func(id int, name string, arguments map[string]any) []byte {
		return mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	}

	adopt := callMCP(t, server, request(1, "project_identifiers_adopt", map[string]any{"project_id": "example", "project_code": "GTW"}))
	adoptResult, ok := adopt["result"].(map[string]any)
	if !ok || adoptResult["isError"] != false {
		t.Fatalf("identifier adoption failed: %#v", adopt)
	}
	adopted, ok := adoptResult["structuredContent"].(map[string]any)
	if !ok || adopted["identifiers"].(map[string]any)["project_code"] != "GTW" {
		t.Fatalf("unexpected adoption result: %#v", adopt)
	}

	read := callMCP(t, server, request(2, "project_identifiers_read", map[string]any{"project_id": "example"}))
	readResult, ok := read["result"].(map[string]any)
	if !ok || readResult["isError"] != false || readResult["structuredContent"].(map[string]any)["project_code"] != "GTW" {
		t.Fatalf("unexpected identifier read result: %#v", read)
	}

	duplicate := callMCP(t, server, request(3, "project_identifiers_adopt", map[string]any{"project_id": "example", "project_code": "GTW"}))
	if result, ok := duplicate["result"].(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("duplicate adoption was accepted: %#v", duplicate)
	}
	secondProject := model.Project{SchemaVersion: 1, ID: "second", RepositoryURL: "git@example.invalid:second.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("b", 40), Status: "active"}
	currentHub, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: secondProject, WriteOptions: service.WriteOptions{ExpectedHubRevision: currentHub}}); err != nil {
		t.Fatal(err)
	}
	duplicateCode := callMCP(t, server, request(6, "project_identifiers_adopt", map[string]any{"project_id": "second", "project_code": "GTW"}))
	if result, ok := duplicateCode["result"].(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("duplicate project code was accepted: %#v", duplicateCode)
	}

	unknown := callMCP(t, server, request(4, "project_identifiers_read", map[string]any{"project_id": "example", "unknown": true}))
	if errObject, ok := unknown["error"].(map[string]any); !ok || errObject["code"] != float64(-32602) {
		t.Fatalf("unknown identifier field was accepted: %#v", unknown)
	}
	malformed := callMCP(t, server, request(5, "project_identifiers_adopt", map[string]any{"project_id": "example", "project_code": "gtw"}))
	if errObject, ok := malformed["error"].(map[string]any); ok {
		if errObject["code"] != float64(-32602) {
			t.Fatalf("malformed project code returned unexpected protocol error: %#v", malformed)
		}
	} else if result, ok := malformed["result"].(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("malformed project code was accepted: %#v", malformed)
	}
}
