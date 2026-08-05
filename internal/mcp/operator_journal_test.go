package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func operatorMCPRequest(t *testing.T, id int, name string, arguments map[string]any) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
}

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
		t.Fatalf("unexpected operator annotations: record=%+v history=%+v checkpoint=%+v", tools["operator_record"].Annotations, tools["operator_history"].Annotations, tools["operator_checkpoint"].Annotations)
	}
	now := time.Now().UTC()
	session := "session"
	validEvent := model.OperatorJournalEvent{SchemaVersion: 1, ID: "EXM-O1", ProjectID: "example", SessionID: &session, Kind: model.OperatorUserTalk, Summary: "context", Content: model.OperatorJournalContent{Facts: []string{"fact"}}, References: model.OperatorJournalReferences{}, Actor: "owner", OccurredAt: now, RecordedAt: now}
	if err := validateOutputValue(tools["operator_record"].OutputSchema, normalizeObject(map[string]any{"event": validEvent, "operation": service.OperationResult{Hub: testOperationHub(), ProjectID: "example", Status: "recorded"}})); err != nil {
		t.Fatalf("operator output schema rejected valid event: %v", err)
	}

	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	adopted, _, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	_ = adopted
	server = &Server{Service: s}
	record := callMCP(t, server, operatorMCPRequest(t, 1, "operator_record", operatorMCPArguments("example", "user_talk", "first context")))
	if result, ok := record["result"].(map[string]any); !ok || result["isError"] != false {
		t.Fatalf("operator record failed: %#v", record)
	}
	checkpointArgs := map[string]any{"project_id": "example", "session_id": nil, "summary": "checkpoint", "content": map[string]any{"decisions": []string{}, "commitments": []string{"keep scope"}, "facts": []string{}, "assumptions": []string{}, "blockers": []string{}, "unresolved": []string{}, "next_actions": []string{}}, "references": map[string]any{"plan_sections": []string{}, "adrs": []string{}, "tasks": []string{}, "runs": []string{}, "commits": []string{}, "identities": []string{}}, "actor": "owner"}
	checkpoint := callMCP(t, server, operatorMCPRequest(t, 2, "operator_checkpoint", checkpointArgs))
	if result, ok := checkpoint["result"].(map[string]any); !ok || result["isError"] != false {
		t.Fatalf("operator checkpoint failed: %#v", checkpoint)
	}
	history := callMCP(t, server, operatorMCPRequest(t, 3, "operator_history", map[string]any{"project_id": "example", "limit": 1}))
	structured := history["result"].(map[string]any)["structuredContent"].(map[string]any)
	if structured["has_more"] != true || len(structured["events"].([]any)) != 1 {
		t.Fatalf("unexpected operator history page: %#v", structured)
	}
	unknown := callMCP(t, server, operatorMCPRequest(t, 4, "operator_record", map[string]any{"project_id": "example", "kind": "user_talk", "summary": "unknown", "content": map[string]any{}, "references": map[string]any{}, "actor": "owner", "unknown": true}))
	if errObject, ok := unknown["error"].(map[string]any); !ok || errObject["code"] != float64(-32602) {
		t.Fatalf("unknown operator field was accepted: %#v", unknown)
	}
	reserved := callMCP(t, server, operatorMCPRequest(t, 5, "operator_record", operatorMCPArguments("example", "operation", "reserved")))
	if result, ok := reserved["result"].(map[string]any); !ok || result["isError"] != true {
		t.Fatalf("reserved operator kind was accepted: %#v", reserved)
	}
}

func testOperationHub() hub.TransactionResult {
	return hub.TransactionResult{Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40), Remote: "origin", Branch: "main", Paths: []string{"gpt-tunnel/v1/projects/example/operator-journal/events/EXM-O1.json"}}
}
