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
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func newSessionTestServer(t *testing.T) *Server {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	hubBare, root, hubHead := testutil.RepoWithBareRemote(t)
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", StateDir: state, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "test", AuthorEmail: "test@example.invalid"}, Projects: map[string]config.ProjectConfig{
		"example": {Root: root, Mirror: filepath.Join(t.TempDir(), "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"},
	}}
	s := service.New(c)
	if _, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}}); err != nil {
		t.Fatal(err)
	}
	return &Server{
		Service:          s,
		AuthorityContext: authority.WithDelivery(context.Background()),
	}
}

func registerMCPTestCodingAgent(t *testing.T, s *service.Service, revision string) string {
	t.Helper()
	registered, result, err := s.AgentRegister(service.WithPlannerWorkflowPolicyAuthority(context.Background()), service.AgentRegisterInput{
		Agent: model.Agent{
			SchemaVersion:        model.AgentSchemaVersion,
			ProjectID:            "example",
			AgentID:              "coding-example",
			Role:                 model.AgentRoleCoding,
			Enabled:              true,
			RecommendedReasoning: model.ReasoningHigh,
		},
		WriteOptions: service.WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if registered.AgentID != "coding-example" || result.Status != "registered" {
		t.Fatalf("unexpected test coding agent registration: %#v %#v", registered, result)
	}
	return result.Hub.After
}

func sessionCall(t *testing.T, server *Server, args map[string]any) map[string]any {
	t.Helper()
	return callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "session", "arguments": args}}))
}

func TestSessionLifecyclePersistsAndBindsProjectRole(t *testing.T) {
	server := newSessionTestServer(t)
	started := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt", "label": "main"}))
	record := started["session"].(map[string]any)
	id := record["session_id"].(string)
	if !strings.HasPrefix(id, "SD-") || len(id) != 11 || record["project_id"] != "example" || record["role"] != "delivery" || record["status"] != "active" {
		t.Fatalf("bad session projection: %#v", record)
	}
	info := genericStructured(t, sessionCall(t, server, map[string]any{"action": "info", "session_id": id}))
	if info["session"].(map[string]any)["session_id"] != id {
		t.Fatalf("info did not reload session: %#v", info)
	}
	updated := genericStructured(t, sessionCall(t, server, map[string]any{"action": "update", "session_id": id, "label": "renamed"}))
	if updated["session"].(map[string]any)["label"] != "renamed" {
		t.Fatalf("update projection=%#v", updated)
	}
	ended := genericStructured(t, sessionCall(t, server, map[string]any{"action": "end", "session_id": id}))
	if ended["session"].(map[string]any)["status"] != "ended" {
		t.Fatalf("end projection=%#v", ended)
	}
	path := filepath.Join(server.Service.Config.StateDir, "sessions", id+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSessionBootstrapCreatesIndependentPlannerAndDeliveryTypedSessions(t *testing.T) {
	server := newSessionTestServer(t)
	planner := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt"}))
	delivery := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt"}))
	plannerID := planner["session"].(map[string]any)["session_id"].(string)
	deliveryID := delivery["session"].(map[string]any)["session_id"].(string)
	if !strings.HasPrefix(plannerID, "SP-") || !strings.HasPrefix(deliveryID, "SD-") || plannerID == deliveryID {
		t.Fatalf("bootstrap did not create independent typed sessions: planner=%q delivery=%q", plannerID, deliveryID)
	}
	if err := authority.RequirePlanner(context.Background()); err == nil {
		t.Fatal("untrusted context acquired planner authority")
	}
	if err := authority.RequirePlannerOrDelivery(authority.WithPlannerOrDelivery(context.Background())); err != nil {
		t.Fatalf("combined bootstrap authority rejected: %v", err)
	}
}

func TestDefaultDeliveryRootResolvesPlannerAndDeliverySessionsAndLegacyDeliveryTool(t *testing.T) {
	server := newSessionTestServer(t)
	if err := server.RegisterGenericAction(GenericAction{
		Path:          "test/planner-only",
		Description:   "Planner-session-only regression action",
		InputSchema:   obj(map[string]any{}),
		OutputSchema:  closedOutput(map[string]any{"role": outputString()}, "role"),
		AuthorityRole: durableSession.RolePlanner,
		Authority:     authority.RequirePlanner,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := authority.RequirePlanner(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"role": durableSession.RolePlanner}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterGenericAction(GenericAction{
		Path:          "test/delivery-only",
		Description:   "Delivery-session-only regression action",
		InputSchema:   obj(map[string]any{}),
		OutputSchema:  closedOutput(map[string]any{"role": outputString()}, "role"),
		AuthorityRole: durableSession.RoleDelivery,
		Authority:     authority.RequireDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := authority.RequireDelivery(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"role": durableSession.RoleDelivery}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	planner := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt",
	}))
	plannerID := planner["session"].(map[string]any)["session_id"].(string)
	if !strings.HasPrefix(plannerID, "SP-") {
		t.Fatalf("planner bootstrap did not return SP session: %q", plannerID)
	}
	plannerCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": plannerID, "action": "test/planner-only", "input": map[string]any{},
		}},
	})))
	if plannerCall["is_error"] != false || plannerCall["result"].(map[string]any)["role"] != durableSession.RolePlanner {
		t.Fatalf("planner session-authorized call failed: %#v", plannerCall)
	}

	delivery := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
	}))
	deliveryID := delivery["session"].(map[string]any)["session_id"].(string)
	if !strings.HasPrefix(deliveryID, "SD-") {
		t.Fatalf("delivery bootstrap did not return SD session: %q", deliveryID)
	}
	deliveryCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session_id": deliveryID, "action": "test/delivery-only", "input": map[string]any{},
		}},
	})))
	if deliveryCall["is_error"] != false || deliveryCall["result"].(map[string]any)["role"] != durableSession.RoleDelivery {
		t.Fatalf("delivery session-authorized call failed: %#v", deliveryCall)
	}

	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "delivery_handoff_list", "arguments": map[string]any{"project_id": "example"}},
	})))
	if legacy["handoffs"] == nil {
		t.Fatalf("legacy direct Delivery typed tool did not succeed: %#v", legacy)
	}
}

func TestSessionStartValidatesProjectRoleAndTypeBeforeCreation(t *testing.T) {
	server := newSessionTestServer(t)
	for _, args := range []map[string]any{
		{"action": "start", "project_id": "missing", "role": "delivery", "session_type": "chatgpt"},
		{"action": "start", "project_id": "example", "role": "operator", "session_type": "chatgpt"},
		{"action": "start", "project_id": "example", "role": "delivery", "session_type": "unknown"},
	} {
		response := sessionCall(t, server, args)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("invalid start was accepted: %#v", response)
		}
	}
	entries, err := os.ReadDir(filepath.Join(server.Service.Config.StateDir, "sessions"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("invalid starts created sessions: %d", len(entries))
	}
}

func TestGenericCallRequiresSessionAndInheritsProject(t *testing.T) {
	server := newSessionTestServer(t)
	var seen json.RawMessage
	if err := server.RegisterGenericAction(GenericAction{
		Path:         "test/project",
		Description:  "Project-bound test action",
		InputSchema:  obj(map[string]any{"project_id": str("Inherited project")}, "project_id"),
		OutputSchema: closedOutput(map[string]any{"project_id": outputString()}, "project_id"),
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			seen = append(seen[:0], raw...)
			var input struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return map[string]any{"project_id": input.ProjectID}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	missing := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"action": "test/project", "input": map[string]any{}}}}))
	if result, ok := missing["result"].(map[string]any); ok || result != nil {
		t.Fatalf("missing session did not fail schema validation: %#v", missing)
	}
	start := genericStructured(t, sessionCall(t, server, map[string]any{"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt"}))
	sessionID := start["session"].(map[string]any)["session_id"].(string)
	okCall := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{}}}})))
	if okCall["is_error"] != false || string(seen) != `{"project_id":"example"}` {
		t.Fatalf("project was not inherited: result=%#v input=%s", okCall, seen)
	}
	mismatch := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session_id": sessionID, "action": "test/project", "input": map[string]any{"project_id": "other"}}}})))
	if mismatch["is_error"] != true || !strings.Contains(mismatch["result"].(map[string]any)["error"].(string), "does not match session project") {
		t.Fatalf("project mismatch was not rejected: %#v", mismatch)
	}
	if string(seen) != `{"project_id":"example"}` {
		t.Fatalf("mismatch reached handler: %s", seen)
	}
}

func TestSystemPingRemainsStandaloneAndGenericBatchContinues(t *testing.T) {
	server := newSessionTestServer(t)
	legacy := genericStructured(t, callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "system_ping", "arguments": map[string]any{}}})))
	if legacy["service"] != "gpt-tunnel-gatewayd" {
		t.Fatalf("standalone system_ping failed: %#v", legacy)
	}
	root := callMCP(t, server, mustJSON(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "schema", "arguments": map[string]any{"path": "system"}}}))
	result, ok := root["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("system domain unexpectedly exposed: %#v", root)
	}
}
