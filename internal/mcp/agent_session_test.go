package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestAgentSessionToolsUseRegisteredProjectAndDoNotMutateDurableWorkflow(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\nprompt) printf 'sent\\n' ;;&\ntail) printf 'one\\ntwo\\nthree\\nfour\\nfive\\nsix\\n' ;;&\nsession-status) printf 'Controller: reachable (5ms)\\nAirelay version: 0.1.54\\nProtocol version: 1\\nState: busy\\n⚠ Selected model is at capacity.\\n' ;;&\nesac\n"
	// The fixture shell is intentionally POSIX-compatible; replace the case
	// fall-through markers for shells that do not support ;;&.
	body = strings.ReplaceAll(body, ";;&", ";;")
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	adoptedPolicyRevision := adoptTestWorkflowPolicy(t, s, "example", registered.Hub.After)
	srv := &Server{Service: s}
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	send := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agent_send","arguments":{"project_id":"example","message":"hello"}}}`))
	sendResult := send["result"].(map[string]any)
	if sendResult["isError"] != false || sendResult["structuredContent"].(map[string]any)["delivered"] != true {
		t.Fatalf("send failed: %#v", send)
	}

	tail := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"agent_tail","arguments":{"project_id":"example","lines":4,"skip":2}}}`))
	tailResult := tail["result"].(map[string]any)
	if tailResult["isError"] != false || tailResult["structuredContent"].(map[string]any)["text"] != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("tail failed: %#v", tail)
	}

	status := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"agent_status","arguments":{"project_id":"example"}}}`))
	statusResult := status["result"].(map[string]any)
	statusContent := statusResult["structuredContent"].(map[string]any)
	if statusResult["isError"] != false || statusContent["state"] != "running" || len(statusContent["capacity_warnings"].([]any)) != 1 {
		t.Fatalf("status failed: %#v", status)
	}

	projectStatus := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"project_status","arguments":{"project_id":"example"}}}`))
	projectStatusResult := projectStatus["result"].(map[string]any)
	projectStatusContent := projectStatusResult["structuredContent"].(map[string]any)
	if projectStatusResult["isError"] != false || projectStatusContent["progress"] == nil {
		t.Fatalf("project status aggregation failed: %#v", projectStatus)
	}
	local := projectStatusContent["local"].(map[string]any)
	if _, ok := local["root"]; ok || strings.Contains(string(mustJSON(t, projectStatusContent)), c.Projects["example"].Root) || strings.Contains(string(mustJSON(t, projectStatusContent)), "example_master") {
		t.Fatalf("project status exposed internal project metadata: %#v", projectStatusContent)
	}
	if schema, err := json.Marshal(toolOutputSchemas["project_status"]); err != nil || strings.Contains(string(schema), `"root"`) {
		t.Fatalf("project status schema exposed repository root: %s", schema)
	}

	resume := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"run_resume","arguments":{"run_id":"11111111-1111-4111-8111-111111111111"}}}`))
	resumeResult := resume["result"].(map[string]any)
	if resumeResult["isError"] != true || strings.Contains(string(mustJSON(t, resumeResult)), "example_master") {
		t.Fatalf("run_resume MCP call did not use the safe service boundary: %#v", resume)
	}

	unknown := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"agent_send","arguments":{"project_id":"example","message":"hello","session_key":"arbitrary"}}}`))
	if unknown["error"].(map[string]any)["code"] != float64(-32602) {
		t.Fatalf("caller-supplied session key was accepted: %#v", unknown)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || before != after || adoptedPolicyRevision != before {
		t.Fatalf("direct agent tools mutated durable workflow: before=%s after=%s err=%v", before, after, err)
	}
}

func TestTaskReadMCPRetainsExecutionOnlyPaths(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\nprompt) printf 'sent\\n' ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
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
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Packet", Objective: "Retain execution paths", Slug: "packet", AcceptanceCriteria: []string{"packet"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: adopted.Hub.After}})
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
	response := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_read","arguments":{"task_id":"`+task.ID+`"}}}`))
	result := response["result"].(map[string]any)
	packet := result["structuredContent"].(map[string]any)
	if result["isError"] != false || packet["repository_root"] != projectRoot || packet["completion_path"] != nil {
		t.Fatalf("task packet exposed a completion destination: %#v", response)
	}
	if !strings.Contains(packet["text"].(string), "gpt-tunnel run finalize "+run.ID) || strings.Contains(packet["text"].(string), run.CompletionPath) || strings.Contains(packet["text"].(string), "write-completion") {
		t.Fatalf("task packet did not require the canonical writer: %#v", response)
	}
	packetRun := packet["run"].(map[string]any)
	if _, exists := packetRun["completion_path"]; exists {
		t.Fatalf("task packet run exposed a completion destination: %#v", packetRun)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
