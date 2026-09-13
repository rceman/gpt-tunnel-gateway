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
		Title:       "TSK571 runtime identity task",
		Summary:     "Task used by the runtime identity transport tests.",
		Objective:   "Exercise role-bound runtime dispatch.",
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
		"clientInfo":      map[string]any{"name": "tsk571-runtime-identity", "version": "1"},
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
	var ref *string
	if role != durableSession.RoleAgent {
		value := runtimeKey
		ref = &value
	}
	record, err := mcpSQLiteSessionStore(t, f.server.Service).Create(durableSession.CreateInput{
		ProjectID: "example", ProjectCode: "EXM", Role: role, SessionType: durableSession.SessionTypeChatGPT, SessionRef: ref,
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

func assertTSK571Resolved(t *testing.T, structured map[string]any) {
	t.Helper()
	if structured["ok"] == true {
		return
	}
	message := tsk571ErrorMessage(t, structured)
	for _, fragment := range []string{"RUNTIME_", "managed runtime identity", "multiple active role-bound Sessions"} {
		if strings.Contains(message, fragment) {
			t.Fatalf("action stopped at runtime identity boundary: %q", message)
		}
	}
}

func TestTSK571WorkerAndLeadRuntimeIdentityOverHTTP(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker, durableSession.RoleLead}, true, true)
	worker := fixture.sessions[durableSession.RoleWorker]
	lead := fixture.sessions[durableSession.RoleLead]
	if worker == lead {
		t.Fatalf("Worker and Lead Sessions were merged: %q", worker)
	}

	read := fixture.call(t, fixture.runtime, "task/read", map[string]any{"key": fixture.task.ID})
	if read["ok"] != true {
		t.Fatalf("Worker task/read failed: %#v", read)
	}
	readResult, ok := read["result"].(map[string]any)
	if !ok || readResult["key"] != fixture.task.ID {
		t.Fatalf("Worker task/read result=%#v", read)
	}
	encoded, _ := json.Marshal(readResult)
	if strings.Contains(string(encoded), fixture.runtime) || strings.Contains(string(encoded), worker) || strings.Contains(string(encoded), lead) {
		t.Fatalf("Task output exposed runtime or durable Session identity: %s", encoded)
	}

	for _, action := range []string{"task/current", "task/submit-code", "task/submit-tests", "task/submit-rebase"} {
		result := fixture.call(t, fixture.runtime, action, map[string]any{})
		assertTSK571Resolved(t, result)
	}
	for _, action := range []string{"task/status", "task/review"} {
		input := map[string]any{"key": fixture.task.ID}
		if action == "task/review" {
			input["stage"] = "code"
		}
		result := fixture.call(t, fixture.runtime, action, input)
		assertTSK571Resolved(t, result)
	}
}

func TestTSK571SharedPhysicalAgentRoleSpecificSessionsAndAmbiguity(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker, durableSession.RoleLead}, true, true)
	workerResult := fixture.call(t, fixture.runtime, "task/read", map[string]any{"key": fixture.task.ID})
	if workerResult["ok"] != true {
		t.Fatalf("shared runtime Worker resolution failed: %#v", workerResult)
	}
	leadResult := fixture.call(t, fixture.runtime, "task/status", map[string]any{"key": fixture.task.ID})
	assertTSK571Resolved(t, leadResult)
	fixture.addSession(t, "example", "EXM", durableSession.RoleWorker, fixture.runtime)
	ambiguous := fixture.call(t, fixture.runtime, "task/read", map[string]any{"key": fixture.task.ID})
	message := tsk571ErrorMessage(t, ambiguous)
	if !strings.Contains(message, "RUNTIME_SESSION_AMBIGUOUS") {
		t.Fatalf("shared runtime Worker Session did not fail closed: %q", message)
	}
}

func TestTSK571RuntimeIdentityFailsClosedOverHTTP(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	tests := []struct {
		name   string
		make   func(*testing.T) *tsk571HTTPFixture
		action string
		input  map[string]any
		want   string
	}{
		{
			name: "no managed Agent binding",
			make: func(t *testing.T) *tsk571HTTPFixture {
				return newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, false, true)
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_IDENTITY_UNAVAILABLE",
		},
		{
			name: "mismatched runtime binding",
			make: func(t *testing.T) *tsk571HTTPFixture {
				fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
				fixture.server.Service.Config.ProjectAgentBindings["example"]["coding-example"] = config.AgentBinding{SessionKey: "different-runtime", Profile: "coding"}
				return fixture
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_IDENTITY_UNAVAILABLE",
		},
		{
			name: "ambiguous Agent match",
			make: func(t *testing.T) *tsk571HTTPFixture {
				fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
				revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_ = seedTSK571Agent(t, fixture.server.Service, revision, "coding-secondary", true)
				fixture.server.Service.Config.ProjectAgentBindings["example"]["coding-secondary"] = config.AgentBinding{SessionKey: fixture.runtime, Profile: "coding"}
				return fixture
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_IDENTITY_AMBIGUOUS",
		},
		{
			name: "ambiguous role Session",
			make: func(t *testing.T) *tsk571HTTPFixture {
				fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
				fixture.addSession(t, "example", "EXM", durableSession.RoleWorker, fixture.runtime)
				return fixture
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_SESSION_AMBIGUOUS",
		},
		{
			name: "inactive Session",
			make: func(t *testing.T) *tsk571HTTPFixture {
				fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
				if _, err := mcpSQLiteSessionStore(t, fixture.server.Service).End(fixture.sessions[durableSession.RoleWorker]); err != nil {
					t.Fatal(err)
				}
				return fixture
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_SESSION_UNAVAILABLE",
		},
		{
			name: "cross-project Session",
			make: func(t *testing.T) *tsk571HTTPFixture {
				fixture := newTSK571HTTPFixture(t, nil, true, true)
				fixture.addSession(t, "other", "OTH", durableSession.RoleWorker, fixture.runtime)
				return fixture
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_SESSION_UNAVAILABLE",
		},
		{
			name: "wrong role Session",
			make: func(t *testing.T) *tsk571HTTPFixture {
				return newTSK571HTTPFixture(t, []string{durableSession.RoleLead}, true, true)
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_SESSION_UNAVAILABLE",
		},
		{
			name: "disabled Agent",
			make: func(t *testing.T) *tsk571HTTPFixture {
				return newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, false)
			},
			action: "task/read", input: map[string]any{}, want: "RUNTIME_IDENTITY_UNAVAILABLE",
		},
		{
			name: "Worker unauthorized Planner action",
			make: func(t *testing.T) *tsk571HTTPFixture {
				return newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
			},
			action: "task/dispatch", input: map[string]any{"key": "EXM-TSK-NOT-DISPATCHED"}, want: "RUNTIME_SESSION_UNAVAILABLE",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := tc.make(t)
			message := tsk571ErrorMessage(t, fixture.call(t, fixture.runtime, tc.action, tc.input))
			if !strings.Contains(message, tc.want) {
				t.Fatalf("error=%q, want %q", message, tc.want)
			}
		})
	}
}

func TestTSK571DirectDurableRoleSessionsRequireRuntimeIdentity(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker, durableSession.RoleLead, durableSession.RoleAdvisor}, true, true)
	cases := []struct {
		role   string
		action string
		input  map[string]any
	}{
		{durableSession.RoleWorker, "task/read", map[string]any{"key": fixture.task.ID}},
		{durableSession.RoleLead, "task/status", map[string]any{"key": fixture.task.ID}},
		{durableSession.RoleAdvisor, "agent/status", map[string]any{}},
	}
	for _, tc := range cases {
		message := tsk571ErrorMessage(t, fixture.call(t, fixture.sessions[tc.role], tc.action, tc.input))
		if !strings.Contains(message, "managed runtime identity is required") {
			t.Fatalf("direct %s Session error=%q", tc.role, message)
		}
	}
}

func TestTSK571PlannerAndLegacyAgentDirectSessionsRemainValid(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleAgent}, true, true)
	for _, role := range []string{durableSession.RolePlanner, durableSession.RoleAgent} {
		result := fixture.call(t, fixture.sessions[role], "task/read", map[string]any{"key": fixture.task.ID})
		if result["ok"] != true {
			t.Fatalf("direct %s Session task/read failed: %#v", role, result)
		}
	}
}

func TestTSK571AgentOperationsStayBoundToResolvedRuntime(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	wrongAgent := fixture.call(t, fixture.runtime, "agent/status", map[string]any{"agent": "other-agent"})
	message := tsk571ErrorMessage(t, wrongAgent)
	if !strings.Contains(message, "managed runtime is not authorized for the requested Agent") {
		t.Fatalf("wrong Agent selector was not rejected: %q", message)
	}
	wrongSession := fixture.call(t, fixture.runtime, "agent/tail", map[string]any{"session": "SA-EXM-ABCD"})
	message = tsk571ErrorMessage(t, wrongSession)
	if !strings.Contains(message, "managed runtime is not authorized for the requested Agent Session") {
		t.Fatalf("wrong Agent Session selector was not rejected: %q", message)
	}
	status := fixture.call(t, fixture.runtime, "agent/status", map[string]any{})
	if status["ok"] != true {
		t.Fatalf("resolved runtime agent/status failed: %#v", status)
	}
}

func TestTSK571NewRoleSessionIDsRemainDistinct(t *testing.T) {
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}, true, true)
	seen := map[string]string{}
	for _, role := range []string{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker} {
		id := fixture.sessions[role]
		if !strings.HasPrefix(id, durableSession.SessionIDPrefixAgent+"-") {
			t.Fatalf("%s Session ID=%q does not use the managed role Session ID family", role, id)
		}
		if previous, ok := seen[id]; ok {
			t.Fatalf("%s Session merged with %s as %q", role, previous, id)
		}
		seen[id] = role
	}
}

func TestTSK571RuntimeFailureDiagnosticsRemainSpecific(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, false, true)
	message := tsk571ErrorMessage(t, fixture.call(t, fixture.runtime, "task/read", map[string]any{}))
	if !strings.Contains(message, "RUNTIME_IDENTITY_UNAVAILABLE") || strings.Contains(message, "RUNTIME_SESSION_AMBIGUOUS") {
		t.Fatalf("runtime failure diagnostics=%q", message)
	}
}
