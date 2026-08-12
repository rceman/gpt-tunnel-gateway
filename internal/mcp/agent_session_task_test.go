package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskReadMCPRetainsExecutionOnlyPaths(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\nprompt) printf 'sent\\n' ;;\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, ProjectAgentBindings: map[string]map[string]config.AgentBinding{"example": {"coding-example": {SessionKey: "example_master"}}}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	policyRevision := adoptTestWorkflowPolicy(t, s, "example", registered.Hub.After)
	_, adopted, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: policyRevision}})
	if err != nil {
		t.Fatal(err)
	}
	agentRevision := registerMCPTestCodingAgent(t, s, adopted.Hub.After)
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Packet", Objective: "Retain execution paths", Slug: "packet", AcceptanceCriteria: []string{"packet"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: agentRevision}})
	if err != nil {
		t.Fatal(err)
	}
	title, summary, objective := "Packet", "Packet", "Retain execution paths"
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, CurrentObjective: &objective, ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	servicePacket, err := s.TaskRead(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOutputValue(toolOutputSchemas["task_read"], normalizeObject(service.PublicTaskPacketView(servicePacket))); err != nil {
		if packetErr := validateOutputValue(taskPacketOutputSchema(), normalizeObject(service.PublicTaskPacketView(servicePacket))); packetErr != nil {
			t.Fatalf("service task packet violates task_read schema: %v (packet: %v); packet=%#v", err, packetErr, service.PublicTaskPacketView(servicePacket))
		}
		t.Fatalf("service task packet violates task_read schema: %v; packet=%#v", err, service.PublicTaskPacketView(servicePacket))
	}
	response := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_read","arguments":{"task_id":"`+task.ID+`"}}}`))
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("task_read response missing result: %#v", response)
	}
	packet, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("task_read response missing structured packet: %#v", response)
	}
	if result["isError"] != false || packet["repository_root"] != projectRoot || packet["completion_path"] != nil {
		t.Fatalf("task packet exposed a completion destination: %#v", response)
	}
	if !strings.Contains(packet["text"].(string), "gpt-tunnel run finalize "+run.ID+" --summary <text>") || strings.Contains(packet["text"].(string), run.CompletionPath) || strings.Contains(packet["text"].(string), "write-completion") || strings.Contains(packet["text"].(string), "--completion-file") {
		t.Fatalf("task packet did not require the canonical writer: %#v", response)
	}
	packetRun := packet["run"].(map[string]any)
	if _, exists := packetRun["completion_path"]; exists {
		t.Fatalf("task packet run exposed a completion destination: %#v", packetRun)
	}
}
