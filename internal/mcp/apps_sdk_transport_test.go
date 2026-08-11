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

func TestToolCallAcceptsBoundedProtocolMeta(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_ping","arguments":{},"_meta":{"openai/locale":"en","nested":{"request":"value"}}}}`))
	if response["error"] != nil {
		t.Fatalf("protocol _meta was rejected: %#v", response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("unexpected result: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["version"] != "0.6.11" {
		t.Fatalf("unexpected structured result: %#v", result)
	}
}

func TestPlainTextToolResultRemainsUnstructured(t *testing.T) {
	tool := Tool{}
	result := toolResult(tool, "Warning: Controller\n⚠ Selected model is at capacity.\n", false)
	if result["isError"] != false {
		t.Fatalf("plain text result marked error: %#v", result)
	}
	content := result["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "Warning: Controller\n⚠ Selected model is at capacity.\n" {
		t.Fatalf("unexpected plain text content: %#v", result)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("plain text result has structuredContent: %#v", result)
	}
}

func TestRunAgentTailToolCallUsesLiveServiceAndPlainTextTransport(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\n*) exit 0 ;;\nesac\n"), 0o700); err != nil {
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
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Tail", Objective: "Inspect tail", Slug: "tail", AcceptanceCriteria: []string{"tail"}, OperationClass: "implementation", CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: agentRevision}})
	if err != nil {
		t.Fatal(err)
	}
	title, summary, objective, activeTask := "Tail", "Tail", "Tail", task.ID
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, CurrentObjective: &objective, ActiveTaskID: &activeTask, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '⚠ Selected model is at capacity.\\nworkspace status\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	response := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`"}}}`))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("unexpected tail result: %#v", response)
	}
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content=%#v", content)
	}
	item := content[0].(map[string]any)
	if item["type"] != "text" || !strings.Contains(item["text"].(string), "Selected model is at capacity") {
		t.Fatalf("tail text=%#v", item)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || !strings.Contains(structured["text"].(string), "Selected model is at capacity") {
		t.Fatalf("structured tail=%#v", result)
	}
	explicit := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`","lines":9}}}`))
	explicitResult, ok := explicit["result"].(map[string]any)
	if !ok || explicitResult["isError"] != false || !strings.Contains(explicitResult["content"].([]any)[0].(map[string]any)["text"].(string), "workspace status") {
		t.Fatalf("unexpected explicit tail result: %#v", explicit)
	}
	if structured, ok := explicitResult["structuredContent"].(map[string]any); !ok || !strings.Contains(structured["text"].(string), "workspace status") {
		t.Fatalf("explicit structured tail=%#v", explicitResult)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'tail failure output\\n'\nprintf 'example_master CONTROL_PLANE_API_KEY=secret-marker\\n' >&2\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	assertToolError := func(body []byte, want string) {
		t.Helper()
		response := callMCP(t, &Server{Service: s}, body)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("unexpected error result: %#v", response)
		}
		content := result["content"].([]any)
		text := content[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, want) {
			t.Fatalf("error text=%q want=%q", text, want)
		}
		if _, ok := result["structuredContent"]; ok {
			t.Fatalf("tool error exposed structured content: %#v", result)
		}
		if len(text) > 512 {
			t.Fatalf("error text is not bounded: %d", len(text))
		}
		for _, forbidden := range []string{"example_master", "CONTROL_PLANE_API_KEY", "secret-marker"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("MCP error leaked %q: %q", forbidden, text)
			}
		}
	}
	assertToolError([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`","lines":9}}}`), "Airelay tail failed")
	assertToolError([]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"missing"}}}`), "run not found")
	assertToolError([]byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`","lines":201}}}`), "invalid tail line count")
}
