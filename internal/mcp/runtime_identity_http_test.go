package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type tsk571HTTPFixture struct {
	server    *Server
	http      *httptest.Server
	client    *frozenConnectorClient
	runtime   string
	task      model.TaskAuthoring
	sessions  map[string]string
	agentID   string
	projectID string
}

func newTSK571HTTPFixture(t *testing.T, roles []string, bind, enabled bool) *tsk571HTTPFixture {
	t.Helper()
	server := newSessionTestServer(t)
	runtimeKey := "runtime-tsk571"
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revision = seedTSK571Agent(t, server.Service, revision, "coding-example", enabled)
	server.Service.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{}
	if bind {
		server.Service.Config.ProjectAgentBindings["example"] = map[string]config.AgentBinding{
			"coding-example": {SessionKey: runtimeKey, Profile: "coding"},
		}
	}
	server.Service.Airelay.Command = seedTSK571Airelay(t)
	server.Service.Config.AirelayCommand = server.Service.Airelay.Command
	task, _, err := server.Service.TaskLifecycleCreate(context.Background(), service.TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "TSK571 durable Session task",
		Summary:     "Task used by durable Session transport tests.",
		Objective:   "Exercise role-bound durable Session dispatch.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	}, "tsk571-http-task")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &tsk571HTTPFixture{
		server:    server,
		runtime:   runtimeKey,
		task:      task,
		sessions:  map[string]string{},
		agentID:   "coding-example",
		projectID: "example",
	}
	for _, role := range roles {
		fixture.sessions[role] = fixture.createSession(t, role, runtimeKey)
	}
	fixture.http = httptest.NewServer(server.Router())
	t.Cleanup(fixture.http.Close)
	fixture.client = &frozenConnectorClient{
		http:     fixture.http.Client(),
		endpoint: fixture.http.URL + "/mcp",
		methods:  map[string]int{},
	}
	initialized := fixture.client.request(t, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tsk629-durable-session", "version": "1"},
	})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	fixture.client.notify(t, "notifications/initialized")
	return fixture
}

func seedTSK571Agent(t *testing.T, s *service.Service, revision, agentID string, enabled bool) string {
	t.Helper()
	now := time.Now().UTC()
	agent := model.Agent{
		SchemaVersion:        model.AgentSchemaVersion,
		ProjectID:            "example",
		AgentID:              agentID,
		Role:                 model.AgentRoleCoding,
		Enabled:              enabled,
		RecommendedReasoning: model.ReasoningHigh,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed TSK571 coding Agent", func(worktree string) ([]string, error) {
		path := filepath.ToSlash(filepath.Join(hub.ProtocolRoot, "projects", "example", "agents", agentID+".json"))
		if err := hub.WriteJSON(worktree, path, agent); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.UpsertLocalAgent(context.Background(), sqlitestore.LocalAgent{
		ProjectID: "example", AgentID: agentID, Payload: payload, UpdatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return tx.After
}

func seedTSK571Airelay(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = \"session-status\" ]; then\n  if [ \"$3\" = \"--json\" ]; then\n    printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"\n  else\n    printf 'Controller: reachable\\nState: idle\\n'\n  fi\n  exit 0\nfi\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f *tsk571HTTPFixture) createSession(t *testing.T, role, runtimeKey string) string {
	t.Helper()
	value := runtimeKey
	record, err := mcpSQLiteSessionStore(t, f.server.Service).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: role, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &value,
	})
	if err != nil {
		t.Fatalf("create %s Session: %v", role, err)
	}
	return record.ID
}

func (f *tsk571HTTPFixture) addSession(t *testing.T, projectID, projectCode, role, runtimeKey string) string {
	t.Helper()
	value := runtimeKey
	record, err := mcpSQLiteSessionStore(t, f.server.Service).Create(durableSession.CreateInput{
		ProjectID: projectID, ProjectCode: projectCode, Role: role, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &value,
	})
	if err != nil {
		t.Fatalf("create %s Session: %v", role, err)
	}
	return record.ID
}

func (f *tsk571HTTPFixture) call(t *testing.T, session, action string, input map[string]any) map[string]any {
	t.Helper()
	return frozenResult(t, f.client.request(t, "tools/call", map[string]any{
		"name": "call",
		"arguments": map[string]any{
			"session": session,
			"action":  action,
			"input":   input,
		},
	}))
}

func tsk571ErrorMessage(t *testing.T, structured map[string]any) string {
	t.Helper()
	if structured["ok"] == true {
		t.Fatalf("expected generic action failure, got %#v", structured)
	}
	errorValue, ok := structured["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing generic action error: %#v", structured)
	}
	message, ok := errorValue["message"].(string)
	if !ok {
		t.Fatalf("missing generic action error message: %#v", structured)
	}
	return message
}

func TestTSK629DurableWorkflowSessionsOverHTTP(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	worker := fixture.sessions[durableSession.RoleWorker]
	lead := fixture.sessions[durableSession.RoleLead]
	if worker == lead {
		t.Fatalf("Worker and Lead Sessions were merged: %q", worker)
	}

	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		result := fixture.call(t, fixture.sessions[role], "task/read", map[string]any{"key": fixture.task.ID})
		if result["ok"] != true {
			t.Fatalf("durable %s task/read failed: %#v", role, result)
		}
		encoded, _ := json.Marshal(result["result"])
		if strings.Contains(string(encoded), fixture.runtime) || strings.Contains(string(encoded), worker) || strings.Contains(string(encoded), lead) {
			t.Fatalf("Task output exposed runtime or durable Session identity: %s", encoded)
		}
	}
	installTSK563Airelay(t, fixture)
	leadAwait := fixture.call(t, fixture.sessions[durableSession.RoleLead], "agent/await", map[string]any{"agent": fixture.agentID, "seconds": 1})
	if leadAwait["ok"] != true {
		t.Fatalf("durable Lead agent/await failed: %#v", leadAwait)
	}

	runtimeResult := fixture.call(t, fixture.runtime, "task/read", map[string]any{"key": fixture.task.ID})
	if runtimeResult["ok"] != false || !strings.Contains(tsk571ErrorMessage(t, runtimeResult), "durable Session") {
		t.Fatalf("runtime-key authentication was not rejected: %#v", runtimeResult)
	}
}

func TestTSK629SharedPhysicalAgentRoleSpecificSessionsRemainDistinct(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker, durableSession.RoleLead}, true, true)
	for _, role := range []string{durableSession.RoleWorker, durableSession.RoleLead} {
		resolved, err := fixture.server.Service.ResolveRuntimeRoleSessionForSession(context.Background(), fixture.runtime, fixture.sessions[role])
		if err != nil {
			t.Fatalf("resolve %s Session: %v", role, err)
		}
		if resolved.Session.ID != fixture.sessions[role] || resolved.Session.Role != role || resolved.Agent.AgentID != fixture.agentID {
			t.Fatalf("shared Agent merged %s authority: %#v", role, resolved)
		}
	}
	fixture.addSession(t, "example", "EXM", durableSession.RoleWorker, fixture.runtime)
	result := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/read", map[string]any{"key": fixture.task.ID})
	if result["ok"] != true {
		t.Fatalf("exact durable Worker Session did not remain usable after a duplicate ref: %#v", result)
	}
	if runtimeResult := fixture.call(t, fixture.runtime, "task/read", map[string]any{"key": fixture.task.ID}); runtimeResult["ok"] == true || !strings.Contains(tsk571ErrorMessage(t, runtimeResult), "durable Session") {
		t.Fatalf("runtime-key failure was not a durable Session rejection: %#v", runtimeResult)
	}
}

func TestTSK629InvalidPublicSessionAuthenticationFailsClosed(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
	for _, sessionID := range []string{fixture.runtime, "HOM_EXM_W_zzzzz"} {
		result := fixture.call(t, sessionID, "task/read", map[string]any{"key": fixture.task.ID})
		if result["ok"] != false || !strings.Contains(tsk571ErrorMessage(t, result), "durable Session") {
			t.Fatalf("invalid public Session %q was not rejected: %#v", sessionID, result)
		}
	}
	if _, err := mcpSQLiteSessionStore(t, fixture.server.Service).End(fixture.sessions[durableSession.RoleWorker]); err != nil {
		t.Fatal(err)
	}
	inactive := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/read", map[string]any{"key": fixture.task.ID})
	if inactive["ok"] != false {
		t.Fatalf("inactive durable Session was accepted: %#v", inactive)
	}
	crossProject := fixture.addSession(t, "other", "OTH", durableSession.RoleWorker, fixture.runtime)
	cross := fixture.call(t, crossProject, "task/read", map[string]any{"key": fixture.task.ID})
	if cross["ok"] != false {
		t.Fatalf("cross-project durable Session was accepted: %#v", cross)
	}
}

func TestTSK629DirectDurableRoleSessionsAreAccepted(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker, durableSession.RoleLead, durableSession.RoleAdvisor}, true, true)
	for _, role := range []string{durableSession.RoleWorker, durableSession.RoleLead, durableSession.RoleAdvisor} {
		result := fixture.call(t, fixture.sessions[role], "task/read", map[string]any{"key": fixture.task.ID})
		if result["ok"] != true {
			t.Fatalf("direct %s Session was rejected: %#v", role, result)
		}
	}
}

func TestTSK629PlannerDirectSessionWorksAndAgentRoleIsRejected(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner}, true, true)
	result := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "task/read", map[string]any{"key": fixture.task.ID})
	if result["ok"] != true {
		t.Fatalf("direct Planner Session task/read failed: %#v", result)
	}
	ref := "runtime-tsk571"
	if _, err := mcpSQLiteSessionStore(t, fixture.server.Service).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: "agent", SessionType: durableSession.SessionTypeChatGPT, SessionRef: &ref,
	}); err == nil {
		t.Fatal("Agent compatibility role was accepted")
	}
}

func TestTSK629AgentOperationsUseLogicalAgentTarget(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner}, true, true)
	wrongAgent := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "agent/status", map[string]any{"agent": "other-agent"})
	if wrongAgent["ok"] != false {
		t.Fatalf("wrong logical Agent selector was accepted: %#v", wrongAgent)
	}
	wrongSession := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "agent/tail", map[string]any{"session": "HOM_EXM_W_zzzzz"})
	if wrongSession["ok"] != false {
		t.Fatalf("private Session selector was accepted: %#v", wrongSession)
	}
	status := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "agent/status", map[string]any{"agent": fixture.agentID})
	if status["ok"] != true || status["result"].(map[string]any)["agent"] != fixture.agentID {
		t.Fatalf("logical Agent status failed: %#v", status)
	}
}

func TestTSK629NewRoleSessionIDsRemainDistinct(t *testing.T) {
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	seen := map[string]string{}
	for _, role := range []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		id := fixture.sessions[role]
		code, ok := durableSession.WorkflowRoleCode(role)
		if !ok || len(id) != 15 || !strings.HasPrefix(id, "HOM_EXM_") || id[8] != code[0] {
			t.Fatalf("%s Session ID=%q does not use the canonical role Session identity", role, id)
		}
		if previous, ok := seen[id]; ok {
			t.Fatalf("%s Session merged with %s as %q", role, previous, id)
		}
		seen[id] = role
	}
}

func TestTSK629RuntimeFailureDiagnosticsRemainSpecific(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, false, true)
	message := tsk571ErrorMessage(t, fixture.call(t, fixture.runtime, "task/read", map[string]any{}))
	if !strings.Contains(message, "durable Session") || strings.Contains(message, "RUNTIME_IDENTITY") {
		t.Fatalf("runtime failure diagnostics=%q", message)
	}
}
