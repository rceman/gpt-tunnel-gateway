package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	body := "#!/bin/sh\ncase \"$1\" in\nprompt) printf 'sent\\n' ;;&\ntranscript) printf '{\"lines\":[{\"timestamp\":1,\"text\":\"one\"},{\"timestamp\":2,\"text\":\"two\"},{\"timestamp\":3,\"text\":\"three\"},{\"timestamp\":4,\"text\":\"four\"}]}\\n' ;;&\ntail) printf 'one\\ntwo\\nthree\\nfour\\nfive\\nsix\\n' ;;&\nsession-status) printf 'Controller: reachable (5ms)\\nAirelay version: 0.1.54\\nProtocol version: 1\\nState: busy\\n⚠ Selected model is at capacity.\\n' ;;&\nesac\n"
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
	registeredAgentRevision := seedMCPTestCodingAgent(t, s, adoptedPolicyRevision)
	srv := &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	sessionID := genericSession(t, s, "example")
	send := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/prompt", "input": map[string]any{"message": "hello"}}}}))
	sendResult := genericStructured(t, send)
	if sendResult["is_error"] != false {
		t.Fatalf("send failed: %#v", send)
	}
	sendPayload := sendResult["result"].(map[string]any)
	operationID, ok := sendPayload["operation"].(string)
	if !ok || operationID == "" || sendPayload["status"] != "accepted" {
		t.Fatalf("agent/prompt did not return an accepted receipt: %#v", send)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "operation/read", "input": map[string]any{"operation_id": operationID}}}}))
		statusResult := genericStructured(t, status)
		if statusResult["is_error"] == true {
			t.Fatalf("agent/prompt status failed: %#v", status)
		}
		statusPayload := statusResult["result"].(map[string]any)
		if statusPayload["status"] == "completed" || statusPayload["status"] == "failed" {
			if statusPayload["status"] != "completed" || statusPayload["result"].(map[string]any)["delivered"] != true {
				t.Fatalf("agent/prompt worker failed: %#v", status)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	firstHeartbeat, err := srv.awaitWithContinuation(
		service.WithAgentSessionID(context.Background(), sessionID),
		5*time.Millisecond,
		"agent/status",
		mustJSON(t, map[string]any{}),
	)
	if err != nil {
		t.Fatalf("first heartbeat failed: %v", err)
	}
	firstProjection := firstHeartbeat.Continuation.(map[string]any)
	if _, ok := firstProjection["project_id"]; ok {
		t.Fatalf("first heartbeat leaked static project identity: %#v", firstProjection)
	}
	if tail, ok := firstProjection["tail"].([]string); !ok || len(tail) == 0 {
		t.Fatalf("first heartbeat omitted new tail lines: %#v", firstProjection)
	}

	tail := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/tail", "input": map[string]any{"lines": 4}}}}))
	tailResult := genericStructured(t, tail)
	if tailResult["is_error"] != false {
		t.Fatalf("tail failed: %#v", tail)
	}

	status := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/status", "input": map[string]any{"agent": "coding-example"}}}}))
	statusResult := genericStructured(t, status)
	statusContent := statusResult["result"].(map[string]any)
	if statusResult["is_error"] != false || statusContent["agent"] != "coding-example" {
		t.Fatalf("status failed: %#v", status)
	}
	if _, ok := statusContent["status"].(string); !ok {
		t.Fatalf("status omitted compact state: %#v", statusContent)
	}
	secondHeartbeat, err := srv.awaitWithContinuation(
		service.WithAgentSessionID(context.Background(), sessionID),
		5*time.Millisecond,
		"agent/status",
		mustJSON(t, map[string]any{}),
	)
	if err != nil {
		t.Fatalf("unchanged heartbeat failed: %v", err)
	}
	secondProjection := secondHeartbeat.Continuation.(map[string]any)
	if _, ok := secondProjection["tail"]; ok {
		t.Fatalf("unchanged heartbeat repeated tail lines: %#v", secondProjection)
	}
	for _, field := range []string{"project_id", "agent_id", "registered", "enabled", "bound", "role", "schema_version", "usable", "session_state", "tail_count", "tail_has_new_info"} {
		if _, ok := secondProjection[field]; ok {
			t.Fatalf("unchanged heartbeat leaked redundant field %q: %#v", field, secondProjection)
		}
	}

	rules := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session": sessionID, "action": "rules/read", "input": map[string]any{}}}}))
	rulesResult := genericStructured(t, rules)
	if rulesResult["is_error"] != false {
		t.Fatalf("session-bound project rules read failed: %#v", rules)
	}

	unknown := callMCP(t, srv, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "agent/prompt", "input": map[string]any{"message": "hello", "session_key": "arbitrary"}}}}))
	unknownResult := genericStructured(t, unknown)
	if unknownResult["is_error"] != true {
		t.Fatalf("caller-supplied session key was accepted: %#v", unknown)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || before != after || registeredAgentRevision != before {
		t.Fatalf("direct agent tools mutated durable workflow: before=%s after=%s err=%v", before, after, err)
	}
}

func TestTailToolSchemaIsSessionBoundAndCursorFree(t *testing.T) {
	server := &Server{}
	entries := server.genericActionRegistry(server.tools())
	entry, ok := entries["agent/tail"]
	if !ok || !entry.SessionBound {
		t.Fatalf("agent/tail is not session-bound: %#v", entry)
	}
	properties := entry.InputSchema["properties"].(map[string]any)
	if _, ok := properties["project_id"]; ok {
		t.Fatal("agent/tail exposes project_id")
	}
	if _, ok := properties["skip"]; ok {
		t.Fatal("agent/tail exposes skip")
	}
	if _, ok := properties["cursor"]; ok {
		t.Fatal("agent/tail exposes cursor")
	}
	if _, ok := properties["dedupe"]; ok {
		t.Fatal("agent/tail exposes a caller-controlled dedupe override")
	}
	outputProperties := entry.OutputSchema["properties"].(map[string]any)
	for _, field := range []string{"agent", "lines"} {
		if _, ok := outputProperties[field]; !ok {
			t.Fatalf("agent/tail output omits %s: %#v", field, entry.OutputSchema)
		}
	}
	for _, field := range []string{"count", "has_new_info", "overflow", "history_truncated"} {
		if _, ok := outputProperties[field]; ok {
			t.Fatalf("agent/tail output retained legacy field %s: %#v", field, entry.OutputSchema)
		}
	}
}
