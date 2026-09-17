package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

func seedTSK640Agent(t *testing.T, s *service.Service, revision, agentID, workflowRole string, enabled bool) string {
	t.Helper()
	now := time.Now().UTC()
	agent := model.Agent{
		SchemaVersion:        model.AgentSchemaVersion,
		ProjectID:            "example",
		AgentID:              agentID,
		Role:                 model.AgentRoleCoding,
		WorkflowRole:         workflowRole,
		Enabled:              enabled,
		RecommendedReasoning: model.ReasoningHigh,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed TSK640 Agent", func(worktree string) ([]string, error) {
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

func installTSK640Airelay(t *testing.T, s *service.Service) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airelay")
	script := "#!/bin/sh\ncase \"$1\" in\nsession-status)\n  if [ \"$3\" = \"--json\" ]; then\n    printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"\n  else\n    printf 'Controller: reachable\\nState: idle\\n'\n  fi\n  ;;\n*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = path
	s.Config.AirelayCommand = path
	s.Airelay.Timeout = time.Second
}

func newTSK640SubmitServer(t *testing.T, workflowRole string) (*Server, string) {
	t.Helper()
	server := newSessionTestServer(t)
	runtimeKey := "runtime-worker"
	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK640Agent(t, server.Service, revision, "coding-example", workflowRole, true)
	server.Service.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		"example": {"coding-example": {SessionKey: runtimeKey}},
	}
	installTSK640Airelay(t, server.Service)
	return server, runtimeKey
}

func tsk640WorkerSession(t *testing.T, server *Server, projectID, projectCode, runtimeKey string) string {
	t.Helper()
	value := runtimeKey
	record, err := mcpSQLiteSessionStore(t, server.Service).Create(durableSession.CreateInput{
		ProjectID: projectID, ProjectCode: projectCode, Role: durableSession.RoleWorker, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &value,
	})
	if err != nil {
		t.Fatalf("create %s Worker Session: %v", projectID, err)
	}
	return record.ID
}

func tsk640CreateDispatchedTask(t *testing.T, server *Server, idempotencyKey string) model.TaskAuthoring {
	t.Helper()
	task, _, err := server.Service.TaskLifecycleCreate(context.Background(), service.TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "TSK640 Agent CLI submit task",
		Summary:     "Task used by the Agent CLI Task submission transport tests.",
		Objective:   "Exercise server-derived durable Session submission.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	}, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Service.TaskExecutionDispatch(context.Background(), service.TaskExecutionDispatchInput{ProjectID: "example", Key: task.ID}); err != nil {
		t.Fatal(err)
	}
	return task
}

func tsk640SubmitRequest(t *testing.T, server *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

func tsk640SubmitPost(t *testing.T, server *httptest.Server, path, runtimeKey string) (int, map[string]any) {
	t.Helper()
	status, payload := tsk640SubmitRequest(t, server, http.MethodPost, path, `{"runtime":`+string(mustJSON(t, runtimeKey))+`}`)
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode %s response: %v (%s)", path, err, payload)
	}
	return status, body
}

func tsk640SubmitError(t *testing.T, body map[string]any) (string, string) {
	t.Helper()
	if body["ok"] != false {
		t.Fatalf("expected submission failure, got %#v", body)
	}
	failure, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing submission error: %#v", body)
	}
	code, _ := failure["code"].(string)
	message, _ := failure["message"].(string)
	return code, message
}

func tsk640ExecutionStatus(t *testing.T, server *Server, key string) model.TaskExecutionState {
	t.Helper()
	state, found, err := server.Service.Durability.ReadTaskExecutionState(context.Background(), "example", key)
	if err != nil || !found {
		t.Fatalf("read Task execution state: state=%#v found=%v err=%v", state, found, err)
	}
	return state
}

func TestTSK640AgentCLISubmitEndpointsDispatchCanonicalStageActions(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
	tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	task := tsk640CreateDispatchedTask(t, server, "tsk640-mapping-task")

	for _, stage := range []struct {
		path  string
		stage string
	}{
		{path: agentCLISubmitTestsPath, stage: "tests"},
		{path: agentCLISubmitRebasePath, stage: "rebase"},
	} {
		status, body := tsk640SubmitPost(t, httpServer, stage.path, runtimeKey)
		if status != http.StatusOK {
			t.Fatalf("%s HTTP status=%d body=%#v", stage.path, status, body)
		}
		code, message := tsk640SubmitError(t, body)
		if code != "ACTION_FAILED" || !strings.Contains(message, "not accepting a "+stage.stage+" submission") {
			t.Fatalf("%s did not dispatch its canonical action: code=%q message=%q", stage.path, code, message)
		}
	}
	if state := tsk640ExecutionStatus(t, server, task.ID); state.Status != model.TaskExecutionDispatched || state.Stage != "code" {
		t.Fatalf("stage rejection changed Task state: %#v", state)
	}
	dispatched := tsk640ExecutionStatus(t, server, task.ID)

	status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("submit-code status=%d body=%#v", status, body)
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("submit-code result=%#v", body["result"])
	}
	if result["key"] != task.ID || result["stage"] != "code" || result["status"] != model.TaskExecutionAwaitingReview {
		t.Fatalf("submit-code result=%#v", result)
	}
	if _, leaked := result["session"]; leaked {
		t.Fatalf("submit-code result leaked a private selector: %#v", result)
	}
	state := tsk640ExecutionStatus(t, server, task.ID)
	if state.Status != model.TaskExecutionAwaitingReview || state.ExecutionRevision != dispatched.ExecutionRevision+1 {
		t.Fatalf("submit-code did not advance canonical state: %#v", state)
	}
}

func TestTSK640AgentCLISubmitRejectsNonClosedRequests(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
	sessionID := tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	task := tsk640CreateDispatchedTask(t, server, "tsk640-closed-request-task")

	selectors := map[string]string{
		"empty object":       `{}`,
		"session selector":   `{"runtime":"` + runtimeKey + `","session":"` + sessionID + `"}`,
		"action selector":    `{"runtime":"` + runtimeKey + `","action":"task/submit-code"}`,
		"project selector":   `{"runtime":"` + runtimeKey + `","project_id":"example"}`,
		"task selector":      `{"runtime":"` + runtimeKey + `","key":"EXM-TSK1"}`,
		"agent selector":     `{"runtime":"` + runtimeKey + `","agent":"coding-example"}`,
		"trailing object":    `{"runtime":"` + runtimeKey + `"}{"runtime":"other"}`,
		"non-object body":    `["` + runtimeKey + `"]`,
		"whitespace runtime": `{"runtime":" ` + runtimeKey + ` "}`,
		"oversized runtime":  `{"runtime":"` + strings.Repeat("r", maxAgentCLISubmitBody) + `"}`,
	}
	for name, body := range selectors {
		status, payload := tsk640SubmitRequest(t, httpServer, http.MethodPost, agentCLISubmitCodePath, body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s HTTP status=%d payload=%s", name, status, payload)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("%s decode: %v (%s)", name, err, payload)
		}
		if code, _ := tsk640SubmitError(t, decoded); code != "INVALID_REQUEST" {
			t.Fatalf("%s error code=%q", name, code)
		}
	}

	status, _ := tsk640SubmitRequest(t, httpServer, http.MethodGet, agentCLISubmitCodePath, "")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d", status)
	}
	if state := tsk640ExecutionStatus(t, server, task.ID); state.Status != model.TaskExecutionDispatched {
		t.Fatalf("rejected request changed Task state: %#v", state)
	}
}

func TestTSK640AgentCLISubmitIsLoopbackOnly(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
	tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)

	request, err := http.NewRequest(http.MethodPost, httpServer.URL+agentCLISubmitCodePath, strings.NewReader(`{"runtime":`+string(mustJSON(t, runtimeKey))+`}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "example.com"
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("non-loopback Host status=%d", response.StatusCode)
	}
}

func TestTSK640AgentCLISubmitRejectsUnknownAndAmbiguousRuntimes(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
	sessionID := tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)

	status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, "runtime-unknown")
	if status != http.StatusForbidden {
		t.Fatalf("unknown runtime status=%d body=%#v", status, body)
	}
	if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "RUNTIME_IDENTITY_UNAVAILABLE") {
		t.Fatalf("unknown runtime message=%q", message)
	}
	if status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, sessionID); status != http.StatusForbidden {
		t.Fatalf("durable Session ID as runtime status=%d body=%#v", status, body)
	}

	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK640Agent(t, server.Service, revision, "coding-secondary", durableSession.RoleWorker, true)
	server.Service.Config.ProjectAgentBindings["example"]["coding-secondary"] = config.AgentBinding{SessionKey: runtimeKey}
	status, body = tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
	if status != http.StatusForbidden {
		t.Fatalf("ambiguous runtime status=%d body=%#v", status, body)
	}
	if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "RUNTIME_IDENTITY_AMBIGUOUS") {
		t.Fatalf("ambiguous runtime message=%q", message)
	}
}

func TestTSK640AgentCLISubmitRequiresOneActiveWorkerSession(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	t.Run("no worker session", func(t *testing.T) {
		server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
		httpServer := httptest.NewServer(server.Router())
		t.Cleanup(httpServer.Close)
		status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
		if status != http.StatusForbidden {
			t.Fatalf("status=%d body=%#v", status, body)
		}
		if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "RUNTIME_SESSION_UNAVAILABLE") {
			t.Fatalf("message=%q", message)
		}
	})
	t.Run("inactive worker session", func(t *testing.T) {
		server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
		sessionID := tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
		if _, err := mcpSQLiteSessionStore(t, server.Service).End(sessionID); err != nil {
			t.Fatal(err)
		}
		httpServer := httptest.NewServer(server.Router())
		t.Cleanup(httpServer.Close)
		status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
		if status != http.StatusForbidden {
			t.Fatalf("status=%d body=%#v", status, body)
		}
		if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "RUNTIME_SESSION_UNAVAILABLE") {
			t.Fatalf("message=%q", message)
		}
	})
	t.Run("multiple worker sessions", func(t *testing.T) {
		server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
		tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
		tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
		httpServer := httptest.NewServer(server.Router())
		t.Cleanup(httpServer.Close)
		status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
		if status != http.StatusForbidden {
			t.Fatalf("status=%d body=%#v", status, body)
		}
		if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "RUNTIME_SESSION_AMBIGUOUS") {
			t.Fatalf("message=%q", message)
		}
	})
	t.Run("cross-project worker session", func(t *testing.T) {
		server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
		tsk640WorkerSession(t, server, "other", "OTH", runtimeKey)
		httpServer := httptest.NewServer(server.Router())
		t.Cleanup(httpServer.Close)
		status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
		if status != http.StatusForbidden {
			t.Fatalf("status=%d body=%#v", status, body)
		}
		if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "RUNTIME_SESSION_UNAVAILABLE") {
			t.Fatalf("message=%q", message)
		}
	})
}

func TestTSK640AgentCLISubmitAcceptsLegacyAgentWithoutPortableRole(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, "")
	tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	task := tsk640CreateDispatchedTask(t, server, "tsk640-legacy-role-task")
	dispatched := tsk640ExecutionStatus(t, server, task.ID)

	status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("legacy empty workflow role submission status=%d body=%#v", status, body)
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("legacy empty workflow role result=%#v", body["result"])
	}
	if result["key"] != task.ID || result["stage"] != "code" || result["status"] != model.TaskExecutionAwaitingReview {
		t.Fatalf("legacy empty workflow role result=%#v", result)
	}
	state := tsk640ExecutionStatus(t, server, task.ID)
	if state.Status != model.TaskExecutionAwaitingReview || state.ExecutionRevision != dispatched.ExecutionRevision+1 {
		t.Fatalf("legacy empty workflow role did not advance canonical state: %#v", state)
	}
}

func TestTSK640AgentCLISubmitRejectsWrongPortableRole(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleLead)
	tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	task := tsk640CreateDispatchedTask(t, server, "tsk640-wrong-role-task")

	status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, runtimeKey)
	if status != http.StatusForbidden {
		t.Fatalf("status=%d body=%#v", status, body)
	}
	code, message := tsk640SubmitError(t, body)
	if code != "RUNTIME_ROLE_UNAUTHORIZED" || !strings.Contains(message, "portable Worker identity") {
		t.Fatalf("wrong portable role code=%q message=%q", code, message)
	}
	if state := tsk640ExecutionStatus(t, server, task.ID); state.Status != model.TaskExecutionDispatched {
		t.Fatalf("rejected identity changed Task state: %#v", state)
	}
}

func TestTSK640AgentCLISubmitRejectsWrongTaskAssignment(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
	tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	task := tsk640CreateDispatchedTask(t, server, "tsk640-assignment-task")

	revision, err := server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seedTSK640Agent(t, server.Service, revision, "coding-secondary", durableSession.RoleWorker, true)
	server.Service.Config.ProjectAgentBindings["example"]["coding-secondary"] = config.AgentBinding{SessionKey: "runtime-secondary"}
	tsk640WorkerSession(t, server, "example", "EXM", "runtime-secondary")

	status, body := tsk640SubmitPost(t, httpServer, agentCLISubmitCodePath, "runtime-secondary")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%#v", status, body)
	}
	if _, message := tsk640SubmitError(t, body); !strings.Contains(message, "no current Task is assigned to this Worker") {
		t.Fatalf("message=%q", message)
	}
	if state := tsk640ExecutionStatus(t, server, task.ID); state.Status != model.TaskExecutionDispatched {
		t.Fatalf("wrong-assignment submission changed Task state: %#v", state)
	}
}

func TestTSK640OrdinaryMCPCallStillRejectsRuntimeKeyAsSession(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	server, runtimeKey := newTSK640SubmitServer(t, durableSession.RoleWorker)
	tsk640WorkerSession(t, server, "example", "EXM", runtimeKey)
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	task := tsk640CreateDispatchedTask(t, server, "tsk640-mcp-boundary-task")

	client := &frozenConnectorClient{
		http:     httpServer.Client(),
		endpoint: httpServer.URL + "/mcp",
		methods:  map[string]int{},
	}
	initialized := client.request(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "tsk640-agent-cli", "version": "1"}})
	if initialized["error"] != nil {
		t.Fatalf("initialize failed: %#v", initialized)
	}
	client.notify(t, "notifications/initialized")
	structured := frozenResult(t, client.request(t, "tools/call", map[string]any{
		"name": "call",
		"arguments": map[string]any{
			"session": runtimeKey,
			"action":  "task/submit-code",
			"input":   map[string]any{},
		},
	}))
	if structured["ok"] != false {
		t.Fatalf("ordinary /mcp accepted the runtime key as call.session: %#v", structured)
	}
	failure, _ := structured["error"].(map[string]any)
	if message, _ := failure["message"].(string); !strings.Contains(message, "durable Session authentication failed") {
		t.Fatalf("ordinary /mcp runtime-key rejection message=%q", message)
	}
	if state := tsk640ExecutionStatus(t, server, task.ID); state.Status != model.TaskExecutionDispatched {
		t.Fatalf("ordinary /mcp call changed Task state: %#v", state)
	}
}
