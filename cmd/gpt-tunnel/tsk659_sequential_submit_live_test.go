package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK659LiveSequentialSubmitAdmissionsAreTaskScoped(t *testing.T) {
	var firstTaskID, secondTaskID, plannerSessionID string
	gateway := testutil.NewLiveGateway(t, testutil.LiveGatewayHooks{BeforeStart: func(g *testutil.LiveGateway) {
		firstTaskID = seedTSK657LiveGateway(t, g)
		secondTaskID, plannerSessionID = seedTSK659FollowupTask(t, g)
	}})
	defer gateway.Stop()
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatalf("live gateway did not retain daemon ownership: running=%v lock=%v stderr=%s", gateway.DaemonRunning(), gateway.OwnerLockActive(), gateway.DaemonStderr())
	}

	options := testutil.LiveCommandOptions{Env: map[string]string{"AIRELAY_SESSION_KEY": "live-runtime"}}
	first := gateway.MustCLI(t, options, "task", "submit-code")
	var firstOutput map[string]any
	if err := json.Unmarshal([]byte(first.Stdout), &firstOutput); err != nil {
		t.Fatalf("first submit output is not JSON: %v\n%s", err, first.Stdout)
	}
	if firstOutput["key"] != firstTaskID || firstOutput["stage"] != "code" || firstOutput["status"] != model.TaskExecutionAwaitingReview || firstOutput["execution_revision"] != float64(2) {
		t.Fatalf("first submit returned unexpected Task result: %#v", firstOutput)
	}

	dispatched, err := gateway.MCPCall(context.Background(), "call", map[string]any{
		"session": plannerSessionID,
		"action":  "task/dispatch",
		"input":   map[string]any{"key": secondTaskID},
	})
	if err != nil {
		t.Fatalf("live Planner task/dispatch failed: %v", err)
	}
	if dispatched["ok"] != true {
		t.Fatalf("live Planner task/dispatch rejected Task B: %#v", dispatched)
	}
	commitTSK659LiveCandidate(t, gateway, secondTaskID)

	second := gateway.MustCLI(t, options, "task", "submit-code")
	var secondOutput map[string]any
	if err := json.Unmarshal([]byte(second.Stdout), &secondOutput); err != nil {
		t.Fatalf("second submit output is not JSON: %v\n%s", err, second.Stdout)
	}
	if secondOutput["key"] != secondTaskID || secondOutput["key"] == firstOutput["key"] || secondOutput["stage"] != "code" || secondOutput["status"] != model.TaskExecutionAwaitingReview || secondOutput["execution_revision"] != float64(2) {
		t.Fatalf("second submit reused a stale or wrong Task receipt: first=%#v second=%#v", firstOutput, secondOutput)
	}
	if !gateway.DaemonRunning() || !gateway.OwnerLockActive() {
		t.Fatalf("daemon ownership was lost during sequential submissions: running=%v lock=%v stderr=%s", gateway.DaemonRunning(), gateway.OwnerLockActive(), gateway.DaemonStderr())
	}

	evidence, err := json.Marshal(map[string]any{
		"persistence_owner":         "gpt-tunnel-gatewayd",
		"persistence_owner_lock":    gateway.OwnerLockActive(),
		"daemon_running":            gateway.DaemonRunning(),
		"cli_invoker":               "gpt-tunnel",
		"cli_executable":            gateway.OperatorBinary,
		"submit_path":               "/agent-cli/task/submit-code",
		"dispatch_path":             "/mcp call task/dispatch",
		"worker_session_continuity": "live-runtime",
		"task_a":                    firstTaskID,
		"task_a_result_key":         firstOutput["key"],
		"task_b":                    secondTaskID,
		"task_b_result_key":         secondOutput["key"],
		"stale_receipt_rejected":    secondOutput["key"] != firstOutput["key"],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gate16 evidence: %s", evidence)
}

func seedTSK659FollowupTask(t *testing.T, gateway *testutil.LiveGateway) (string, string) {
	t.Helper()
	db, err := sqlitestore.Open(gateway.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close follow-up seed durability: %v", err)
		}
	}()
	s := service.NewWithDurabilityDeferredWorkers(gateway.Config, db)
	task, _, err := s.TaskLifecycleCreate(context.Background(), service.TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Live task-scoped submit follow-up",
		Summary:     "Exercise a second sequential Worker submission.",
		Objective:   "Prove durable submit admissions remain scoped to Task identity.",
		Priority:    model.TaskPriorityP2,
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	}, "tsk659-live-submit-second")
	if err != nil {
		t.Fatalf("create follow-up live Task: %v", err)
	}
	planner, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		Role:        durableSession.RolePlanner,
		SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatalf("create live Planner Session: %v", err)
	}
	return task.ID, planner.ID
}

func commitTSK659LiveCandidate(t *testing.T, gateway *testutil.LiveGateway, taskID string) {
	t.Helper()
	lane, err := gitx.TaskWorktreePath(gateway.StateDir, "example", taskID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lane, "live-tsk659-submit.txt")
	if err := os.WriteFile(path, []byte("task-scoped submit candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, lane, "add", "live-tsk659-submit.txt")
	testutil.Git(t, lane, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "live task-scoped submit candidate")
}
