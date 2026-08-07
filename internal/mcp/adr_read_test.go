package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestMCPADRReadAcceptsCanonicalCompactIDFromCreateAndList(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	c := config.Config{
		SchemaVersion:          1,
		GatewayID:              "test_gateway",
		StateDir:               t.TempDir(),
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           1000,
		DispatchTimeoutSeconds: 5,
		RunTimeoutSeconds:      60,
		AirelayCommand:         "airelay",
		Hub:                    config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		Projects: map[string]config.ProjectConfig{
			"example": {Root: projectRoot, Mirror: t.TempDir() + "/mirror.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
		},
	}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	_, identifiers, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ADRCreate(context.Background(), service.ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: "Compact ADR", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences"}, WriteOptions: service.WriteOptions{ExpectedHubRevision: identifiers.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	adrs, err := s.ADRList(context.Background(), "example")
	if err != nil || len(adrs) != 1 || adrs[0].ID != "EXM-ADR1" {
		t.Fatalf("unexpected ADR list: %#v %v", adrs, err)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "adr_read",
			"arguments": map[string]any{
				"project_id": "example",
				"adr_id":     adrs[0].ID,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := callMCP(t, &Server{Service: s}, body)
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("compact ADR read failed: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["id"] != "EXM-ADR1" {
		t.Fatalf("unexpected compact ADR read: %#v", result)
	}
}
