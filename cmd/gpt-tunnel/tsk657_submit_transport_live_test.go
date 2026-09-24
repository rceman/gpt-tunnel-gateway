package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK657LiveSubmitResponseLossReconcilesDurably(t *testing.T) {
	var seededTaskID string
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{BeforeStart: func(g *testutil.LiveGateway) {
		seededTaskID = seedTSK657LiveGateway(t, g)
	}})
	defer gateway.Stop()
	if !gateway.DaemonRunning() {
		t.Fatalf("gatewayd exited before test\nstderr=%s", gateway.DaemonStderr())
	}

	var dropResponse atomic.Bool
	var requests atomic.Int32
	dropResponse.Store(true)
	backend := &http.Client{Timeout: 20 * time.Second}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<10))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, "http://"+gateway.ListenAddr()+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		request.Header = r.Header.Clone()
		response, err := backend.Do(request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if dropResponse.Load() {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unavailable", http.StatusInternalServerError)
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				return
			}
			_ = connection.Close()
			return
		}
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(payload)
	}))
	defer proxy.Close()
	operatorConfig := gateway.WriteOperatorConfig(t, strings.TrimPrefix(proxy.URL, "http://"))
	version := gateway.RunCLI(testutil.LiveCommandOptions{ConfigPath: operatorConfig}, "version")
	if version.Err != nil || strings.TrimSpace(version.Stdout) == "" {
		t.Fatalf("live CLI smoke failed: %#v", version)
	}
	options := testutil.LiveCommandOptions{
		ConfigPath: operatorConfig,
		Env:        map[string]string{"AIRELAY_SESSION_KEY": "live-runtime"},
	}

	first := gateway.RunCLI(options, "task", "submit-code")
	if first.Err == nil || first.Stdout != "" {
		t.Fatalf("dropped submit response did not fail at transport: %#v", first)
	}
	if first.Stderr != "" && !strings.Contains(first.Stderr, "Gateway Task submission") && !strings.Contains(first.Stderr, "EOF") && !strings.Contains(first.Stderr, "connection reset") {
		t.Fatalf("dropped submit response produced unexpected stderr: %q", first.Stderr)
	}
	if requests.Load() != 1 || !gateway.DaemonRunning() {
		t.Fatalf("proxy requests=%d daemonRunning=%v stderr=%s", requests.Load(), gateway.DaemonRunning(), gateway.DaemonStderr())
	}
	dropResponse.Store(false)
	second := gateway.RunCLI(options, "task", "submit-code")
	if second.Err != nil {
		t.Fatalf("repeat submit did not reconcile: %v\nstderr=%s\nstdout=%s", second.Err, second.Stderr, second.Stdout)
	}
	if requests.Load() != 2 {
		t.Fatalf("proxy requests=%d, want exactly the initial attempt and one reconciling repeat", requests.Load())
	}
	if second.Stdout == "" {
		t.Fatalf("repeat submit produced no output: %#v", second)
	}
	var submitted map[string]any
	if err := json.Unmarshal([]byte(second.Stdout), &submitted); err != nil {
		t.Fatalf("repeat submit output is not JSON: %v\n%s", err, second.Stdout)
	}
	if submitted["key"] != seededTaskID || submitted["status"] != "awaiting_review" || submitted["stage"] != "code" || submitted["execution_revision"] != float64(2) {
		t.Fatalf("repeat submit returned wrong durable outcome: %#v", submitted)
	}
	if !gateway.DaemonRunning() {
		t.Fatalf("gatewayd exited during reconciliation\nstderr=%s", gateway.DaemonStderr())
	}
}

func seedTSK657LiveGateway(t *testing.T, gateway *testutil.LiveGateway) string {
	t.Helper()
	relayScript := filepath.Join(gateway.BaseDir, "airelay")
	script := "#!/bin/sh\ncase \"$1\" in\nsession-status)\n  if [ \"$3\" = \"--json\" ]; then\n    printf '{\"sessionKey\":\"%s\",\"profile\":\"coding\",\"controllerReachable\":true,\"state\":\"idle\"}' \"$2\"\n  else\n    printf 'Controller: reachable\\nState: idle\\n'\n  fi\n  ;;\n*) exit 0 ;;\nesac\n"
	if err := os.WriteFile(relayScript, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	project := gateway.Config.Projects["example"]
	project = config.ProjectConfig{
		Root:              gateway.ProjectRoot,
		Mirror:            filepath.Join(gateway.BaseDir, "mirror.git"),
		Remote:            "origin",
		DefaultBranch:     "main",
		ProjectCode:       "EXM",
		AirelaySessionKey: "live-runtime",
	}
	gateway.Config.AirelayCommand = relayScript
	gateway.Config.Projects["example"] = project
	gateway.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		"example": {"coding-live": {SessionKey: "live-runtime"}},
	}
	data, err := json.Marshal(gateway.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gateway.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(gateway.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	gateway.Config = loaded
	if err := (hub.Store{Config: gateway.Config}).Ensure(context.Background()); err != nil {
		t.Fatalf("initialize disposable Hub: %v", err)
	}

	db, err := sqlitestore.Open(gateway.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close seed durability: %v", err)
		}
	}()
	s := service.NewWithDurabilityDeferredWorkers(gateway.Config, db)
	ctx := context.Background()
	head := strings.TrimSpace(testutil.Git(t, gateway.ProjectRoot, "rev-parse", "HEAD"))
	if _, err := s.ProjectRegister(ctx, service.ProjectRegisterInput{Project: model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "planner", WorkflowCommit: head, Status: "active"}, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubRevision(t, s)}}); err != nil {
		t.Fatalf("register live project: %v", err)
	}
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedSharedRulesFromConfiguration(ctx, configuration, "EXM"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ProjectIdentifiersAdopt(authority.WithPlanner(ctx), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: hubRevision(t, s)}}); err != nil {
		t.Fatalf("adopt live identifiers: %v", err)
	}
	if _, _, err := s.AgentRegister(authority.WithPlanner(ctx), service.AgentRegisterInput{ProjectID: "example", AgentID: "coding-live", WorkflowRole: durableSession.RoleWorker}); err != nil {
		t.Fatalf("register live Worker: %v", err)
	}
	runtimeKey := "live-runtime"
	if _, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleWorker, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &runtimeKey}); err != nil {
		t.Fatalf("create live Worker Session: %v", err)
	}
	task, _, err := s.TaskLifecycleCreate(ctx, service.TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Live bounded submit transport",
		Summary:     "Exercise a real CLI submit through a dropped response.",
		Objective:   "Prove durable admission reconciles response loss.",
		Priority:    model.TaskPriorityP2,
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	}, "tsk657-live-submit")
	if err != nil {
		t.Fatalf("create live Task: %v", err)
	}
	if _, err := s.TaskExecutionDispatch(ctx, service.TaskExecutionDispatchInput{ProjectID: "example", Key: task.ID}); err != nil {
		t.Fatalf("dispatch live Task: %v", err)
	}
	lane, err := gitx.TaskWorktreePath(gateway.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "live-submit.txt"), []byte("bounded submit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lane, "add", "live-submit.txt")
	testutil.Git(t, lane, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "live submit candidate")
	return task.ID
}

func hubRevision(t *testing.T, s *service.Service) string {
	t.Helper()
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
