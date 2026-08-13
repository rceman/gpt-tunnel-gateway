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
	registeredAgentRevision := registerMCPTestCodingAgent(t, s, adoptedPolicyRevision)
	srv := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	sessionID := genericSession(t, s, "example")
	send := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/send", "input": map[string]any{"project_id": "example", "message": "hello"}}}}))
	sendResult := genericStructured(t, send)
	if sendResult["is_error"] != false || sendResult["result"].(map[string]any)["delivered"] != true {
		t.Fatalf("send failed: %#v", send)
	}

	tail := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/tail", "input": map[string]any{"project_id": "example", "lines": 4}}}}))
	tailResult := genericStructured(t, tail)
	if tailResult["is_error"] != false {
		t.Fatalf("tail failed: %#v", tail)
	}

	status := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/status", "input": map[string]any{"project_id": "example", "agent_id": "coding-example"}}}}))
	statusResult := genericStructured(t, status)
	statusContent := statusResult["result"].(map[string]any)
	if statusResult["is_error"] != false || statusContent["agent_id"] != "coding-example" || statusContent["registered"] != true {
		t.Fatalf("status failed: %#v", status)
	}

	projectStatus := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "status", "arguments": map[string]any{"session_id": sessionID}}}))
	projectStatusResult := genericStructured(t, projectStatus)
	projectStatusContent := projectStatusResult["project_status"].(map[string]any)
	if projectStatusContent["progress"] == nil {
		t.Fatalf("session-bound project status aggregation failed: %#v", projectStatus)
	}
	local := projectStatusContent["local"].(map[string]any)
	if _, ok := local["root"]; ok || strings.Contains(string(mustJSON(t, projectStatusContent)), c.Projects["example"].Root) || strings.Contains(string(mustJSON(t, projectStatusContent)), "example_master") {
		t.Fatalf("project status exposed internal project metadata: %#v", projectStatusContent)
	}
	if schema, err := json.Marshal(toolOutputSchemas["project_status"]); err != nil || strings.Contains(string(schema), `"root"`) {
		t.Fatalf("project status schema exposed repository root: %s", schema)
	}

	unknown := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/send", "input": map[string]any{"project_id": "example", "message": "hello", "session_key": "arbitrary"}}}}))
	unknownResult := genericStructured(t, unknown)
	if unknownResult["is_error"] != true {
		t.Fatalf("caller-supplied session key was accepted: %#v", unknown)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || before != after || registeredAgentRevision != before {
		t.Fatalf("direct agent tools mutated durable workflow: before=%s after=%s err=%v", before, after, err)
	}
}

func TestTailToolSchemasExposeOpaqueContinuation(t *testing.T) {
	tools := (&Server{}).tools()
	for _, name := range []string{"agent_tail"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		properties := tool.InputSchema["properties"].(map[string]any)
		cursor := properties["cursor"].(map[string]any)
		if cursor["type"] != "string" || cursor["maxLength"] != 4096 {
			t.Fatalf("%s cursor schema=%#v", name, cursor)
		}
		outputProperties := tool.OutputSchema["properties"].(map[string]any)
		for _, field := range []string{"next_cursor", "has_more"} {
			if _, ok := outputProperties[field]; !ok {
				t.Fatalf("%s output omits %s: %#v", name, field, tool.OutputSchema)
			}
		}
	}
}
