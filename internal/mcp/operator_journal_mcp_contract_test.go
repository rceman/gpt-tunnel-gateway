package mcp

import (
	"context"
	"encoding/json"
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

func operatorMCPArguments(projectID, kind, summary string) map[string]any {
	return map[string]any{
		"project_id": projectID, "session_id": nil, "kind": kind, "summary": summary,
		"content":    map[string]any{"decisions": []string{}, "commitments": []string{}, "facts": []string{"fact"}, "assumptions": []string{}, "blockers": []string{}, "unresolved": []string{}, "next_actions": []string{}},
		"references": map[string]any{"plan_sections": []string{}, "adrs": []string{}, "tasks": []string{}, "runs": []string{}, "commits": []string{}, "identities": []string{}},
		"actor":      "owner",
	}
}

func TestOperatorJournalMCPContractsAndHappyPath(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tools := server.tools()
	for _, name := range []string{"operator_record", "operator_history", "operator_checkpoint"} {
		tool, ok := tools[name]
		if !ok || tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("missing or open operator tool %q", name)
		}
		if _, ok := tool.OutputSchema["properties"]; !ok {
			t.Fatalf("operator tool %q has no output schema", name)
		}
	}
	if tools["operator_record"].Annotations != additiveExternalAnnotations() || tools["operator_checkpoint"].Annotations != additiveExternalAnnotations() || tools["operator_history"].Annotations != readOnlyAnnotations() {
		t.Fatalf("unexpected operator annotations")
	}
	now := time.Now().UTC()
	session := "session"
	validEvent := model.OperatorJournalEvent{SchemaVersion: 1, ID: "EXM-OPR1", ProjectID: "example", SessionID: &session, Kind: model.OperatorUserTalk, Summary: "context", Content: model.OperatorJournalContent{Facts: []string{"fact"}}, References: model.OperatorJournalReferences{}, Actor: "owner", OccurredAt: now, RecordedAt: now}
	if err := validateOutputValue(tools["operator_record"].OutputSchema, normalizeObject(map[string]any{"event": validEvent, "operation": service.OperationResult{Hub: testOperationHub(), ProjectID: "example", Status: "recorded"}})); err != nil {
		t.Fatalf("operator output schema rejected valid event: %v", err)
	}

	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}}); err != nil {
		t.Fatal(err)
	}
	server = &Server{Service: s, AuthorityContext: authority.WithDelivery(context.Background())}
	sessionID := genericSession(t, s, "example")
	request := func(id int, action string, input map[string]any) []byte {
		return mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": action, "input": input}}})
	}
	recorded := genericActionResult(t, callMCP(t, server, request(1, "operator/record", operatorMCPArguments("example", "user_talk", "first context"))))
	if _, ok := recorded["event"].(map[string]any); !ok {
		t.Fatal("operator record omitted event")
	}
	checkpointArgs := map[string]any{"project_id": "example", "session_id": nil, "summary": "checkpoint", "content": map[string]any{"decisions": []string{}, "commitments": []string{"keep scope"}, "facts": []string{}, "assumptions": []string{}, "blockers": []string{}, "unresolved": []string{}, "next_actions": []string{}}, "references": map[string]any{"plan_sections": []string{}, "adrs": []string{}, "tasks": []string{}, "runs": []string{}, "commits": []string{}, "identities": []string{}}, "actor": "owner"}
	genericActionResult(t, callMCP(t, server, request(2, "operator/checkpoint", checkpointArgs)))
	history := genericActionResult(t, callMCP(t, server, request(3, "operator/history", map[string]any{"project_id": "example", "limit": 1})))
	if history["has_more"] != true || len(history["events"].([]any)) != 1 {
		t.Fatalf("unexpected operator history page: %#v", history)
	}
	unknown := genericStructured(t, callMCP(t, server, request(4, "operator/record", map[string]any{"project_id": "example", "kind": "user_talk", "summary": "unknown", "content": map[string]any{}, "references": map[string]any{}, "actor": "owner", "unknown": true})))
	if unknown["is_error"] != true {
		t.Fatalf("unknown operator field was accepted: %#v", unknown)
	}
	reserved := genericStructured(t, callMCP(t, server, request(5, "operator/record", operatorMCPArguments("example", "operation", "reserved"))))
	if reserved["is_error"] != true {
		t.Fatalf("reserved operator kind was accepted: %#v", reserved)
	}
}

func cloneOperatorMCPValue(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
