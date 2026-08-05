package mcp

import (
	"context"
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

func TestRunCancelAcknowledgeNoMutationMCPContract(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tool, ok := server.tools()["run_cancel_acknowledge_no_mutation"]
	if !ok {
		t.Fatal("acknowledgement tool is not registered")
	}
	if !strings.Contains(tool.Description, "clean at its immutable base") || !strings.Contains(tool.Description, "hard-interrupt") {
		t.Fatalf("safety boundary missing from description: %q", tool.Description)
	}
	if tool.Annotations != destructiveExternalAnnotations() {
		t.Fatalf("annotations=%+v", tool.Annotations)
	}
	if !strings.Contains(string(mustJSON(t, tool.InputSchema)), `"additionalProperties":false`) {
		t.Fatalf("input schema is not closed: %#v", tool.InputSchema)
	}
	properties := tool.InputSchema["properties"].(map[string]any)
	if _, ok := properties["run_id"]; !ok {
		t.Fatal("run_id is not advertised")
	}
	if _, ok := properties["expected_hub_revision"]; !ok {
		t.Fatal("expected_hub_revision is not advertised")
	}

	request := func(id int, arguments map[string]any) []byte {
		return mustJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "run_cancel_acknowledge_no_mutation",
				"arguments": arguments,
			},
		})
	}
	missing := callMCP(t, server, request(1, map[string]any{}))
	if errObject, ok := missing["error"].(map[string]any); !ok || errObject["code"] != float64(-32602) {
		t.Fatalf("missing run_id was accepted: %#v", missing)
	}
	unknown := callMCP(t, server, request(2, map[string]any{"run_id": "missing", "unknown": true}))
	if errObject, ok := unknown["error"].(map[string]any); !ok || errObject["code"] != float64(-32602) {
		t.Fatalf("unknown argument was accepted: %#v", unknown)
	}

	serviceError := callMCP(t, server, request(3, map[string]any{"run_id": "missing"}))
	result, ok := serviceError["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("service error was not propagated as a tool error: %#v", serviceError)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("service error exposed structured content: %#v", serviceError)
	}

	if err := validateOutputValue(tool.OutputSchema, normalizeObject(service.OperationResult{ProjectID: "project", TaskID: "task", RunID: "run", Status: "cancelled_no_mutation", Hub: hub.TransactionResult{Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40), Remote: "origin", Branch: "main", Paths: []string{"projects/project/runs/run.json"}}})); err != nil {
		t.Fatalf("canonical success output rejected: %v", err)
	}
}

func TestRunCancelAcknowledgeNoMutationMCPHappyPath(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	airelay := filepath.Join(dir, "airelay")
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nprintf 'dispatch output\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: airelay, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	_, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "MCP cancel acknowledgement", Objective: "Exercise the MCP surface", Slug: "mcp-cancel-ack", AcceptanceCriteria: []string{"cancel"}, CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: stringPtr("MCP cancel acknowledgement"), Summary: stringPtr("MCP cancel acknowledgement"), CurrentObjective: stringPtr("Exercise the MCP surface"), ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(airelay, []byte("#!/bin/sh\nprintf 'cancel acknowledged\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	currentHub, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCancel(context.Background(), run.ID, currentHub); err != nil {
		t.Fatal(err)
	}

	response := callMCP(t, &Server{Service: s}, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "run_cancel_acknowledge_no_mutation",
			"arguments": map[string]any{"run_id": run.ID},
		},
	}))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("MCP acknowledgement failed: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["status"] != "cancelled_no_mutation" {
		t.Fatalf("unexpected MCP acknowledgement result: %#v", response)
	}
	updatedRun, err := s.RunRead(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("run status=%q", updatedRun.Status)
	}
	taskRecord, err := s.TaskReadRecord(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if taskRecord.State.Status != "ready" {
		t.Fatalf("task state=%q", taskRecord.State.Status)
	}
	updatedPlan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if updatedPlan.ActiveTaskID != "" || updatedPlan.ActiveRunID != "" {
		t.Fatalf("plan pointers remain: %#v", updatedPlan)
	}
}

func stringPtr(value string) *string { return &value }
