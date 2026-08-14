package mcp

import (
	"context"
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
		Agent:        model.Agent{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: "coding-example", Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningHigh},
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

func TestSessionInputSchemaAdvertisesCanonicalActionsAndIDs(t *testing.T) {
	schema := sessionInputSchema()
	variants, ok := schema["oneOf"].([]any)
	if !ok || len(variants) != 5 {
		t.Fatalf("session schema variants=%#v", schema["oneOf"])
	}
	wantActions := map[string]bool{"start": true, "list": true, "info": true, "update": true, "end": true}
	for _, raw := range variants {
		variant := raw.(map[string]any)
		properties := variant["properties"].(map[string]any)
		action := properties["action"].(map[string]any)["const"].(string)
		if !wantActions[action] {
			t.Fatalf("unexpected session action variant %q", action)
		}
		delete(wantActions, action)
		if action == "info" || action == "update" || action == "end" {
			idSchema := properties["session_id"].(map[string]any)
			if idSchema["pattern"] != `^(?:S|SP|SD|SA|SW)-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$` {
				t.Fatalf("%s session ID pattern=%v", action, idSchema["pattern"])
			}
		}
	}
	if len(wantActions) != 0 {
		t.Fatalf("missing session actions: %#v", wantActions)
	}
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
	if _, err := os.Stat(filepath.Join(server.Service.Config.StateDir, "sessions", id+".json")); err != nil {
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

func TestSessionListIsSessionlessAndPreservesUpdatedReference(t *testing.T) {
	server := newSessionTestServer(t)
	oldRef := "https://chatgpt.com/c/old"
	planner := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "planner", "session_type": "chatgpt", "session_ref": oldRef,
	}))
	plannerRecord := planner["session"].(map[string]any)
	plannerID := plannerRecord["session_id"].(string)
	delivery := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "start", "project_id": "example", "role": "delivery", "session_type": "chatgpt",
	}))
	deliveryID := delivery["session"].(map[string]any)["session_id"].(string)

	listed := genericStructured(t, sessionCall(t, server, map[string]any{"action": "list"}))
	sessions, ok := listed["sessions"].([]any)
	if !ok || len(sessions) != 2 {
		t.Fatalf("session.list did not discover both active sessions: %#v", listed)
	}
	first := sessions[0].(map[string]any)["session_id"].(string)
	second := sessions[1].(map[string]any)["session_id"].(string)
	if first > second {
		t.Fatalf("session.list ordering is not deterministic: %#v", sessions)
	}

	newRef := "https://chatgpt.com/c/new"
	updated := genericStructured(t, sessionCall(t, server, map[string]any{
		"action": "update", "session_id": plannerID, "session_ref": newRef,
	}))
	if updated["session"].(map[string]any)["session_ref"] != newRef {
		t.Fatalf("session.update did not return the new reference: %#v", updated)
	}
	info := genericStructured(t, sessionCall(t, server, map[string]any{"action": "info", "session_id": plannerID}))
	if info["session"].(map[string]any)["session_ref"] != newRef {
		t.Fatalf("session.info did not preserve the new reference: %#v", info)
	}

	if _, ok := genericStructured(t, sessionCall(t, server, map[string]any{"action": "end", "session_id": deliveryID}))["session"]; !ok {
		t.Fatal("session.end did not return the ended session")
	}
	listed = genericStructured(t, sessionCall(t, server, map[string]any{"action": "list"}))
	sessions = listed["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("session.list included ended session: %#v", listed)
	}
	item := sessions[0].(map[string]any)
	if item["session_id"] != plannerID || item["ref"] != newRef || item["role"] != "planner" || item["project_id"] != "example" {
		t.Fatalf("session.list projection=%#v", item)
	}
}
