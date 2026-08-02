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

	unknown := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"agent_send","arguments":{"project_id":"example","message":"hello","session_key":"arbitrary"}}}`))
	if unknown["error"].(map[string]any)["code"] != float64(-32602) {
		t.Fatalf("caller-supplied session key was accepted: %#v", unknown)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || before != after || registered.Hub.After != before {
		t.Fatalf("direct agent tools mutated durable workflow: before=%s after=%s err=%v", before, after, err)
	}
}
