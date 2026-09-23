package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func installTSK574Airelay(t *testing.T, fixture *tsk571HTTPFixture, state string, reachable bool) string {
	t.Helper()
	dir := t.TempDir()
	command := filepath.Join(dir, "airelay")
	logPath := filepath.Join(dir, "invocations.log")
	reachableValue := "false"
	if reachable {
		reachableValue = "true"
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
session-status)
  if [ "$3" = "--json" ]; then
    printf '{"sessionKey":"%%s","profile":"coding","controllerReachable":%s,"state":"%s"}' "$2"
  else
    printf 'Controller: reachable\nState: %s\n'
  fi
  ;;
start) printf 'start\n' >> '%s' ;;
*) exit 0 ;;
esac
`, reachableValue, state, state, logPath)
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.server.Service.Airelay.Command = command
	fixture.server.Service.Config.AirelayCommand = command
	fixture.server.Service.Airelay.Timeout = time.Second
	return logPath
}

func tsk574SessionIDs(t *testing.T, fixture *tsk571HTTPFixture) []string {
	t.Helper()
	records, err := durableSession.NewStoreWithDurability(fixture.server.Service.Durability).List()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	sort.Strings(ids)
	return ids
}

func tsk574CreateTask(t *testing.T, fixture *tsk571HTTPFixture, idempotencyKey string) model.TaskAuthoring {
	t.Helper()
	task, _, err := fixture.server.Service.TaskLifecycleCreate(context.Background(), service.TaskAuthoringCreateInput{
		ProjectID:   fixture.projectID,
		Title:       "TSK574 Worker binding task " + idempotencyKey,
		Summary:     "Task used by the Worker binding lifecycle tests.",
		Objective:   "Exercise persistent Worker lane authority.",
		Priority:    model.TaskPriorityP2,
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	}, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func tsk574Dispatch(t *testing.T, fixture *tsk571HTTPFixture, taskID string) service.TaskExecutionPublicOutput {
	t.Helper()
	output, err := fixture.server.Service.TaskExecutionDispatch(context.Background(), service.TaskExecutionDispatchInput{
		ProjectID: fixture.projectID,
		Key:       taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func tsk574MarkDone(t *testing.T, fixture *tsk571HTTPFixture, taskID string) {
	t.Helper()
	state, found, err := fixture.server.Service.Durability.ReadTaskExecutionState(context.Background(), fixture.projectID, taskID)
	if err != nil || !found {
		t.Fatalf("read Task execution state: state=%#v found=%v err=%v", state, found, err)
	}
	previousRevision := state.ExecutionRevision
	state.Status = model.TaskExecutionDone
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	if err := fixture.server.Service.Durability.UpdateTaskExecutionState(context.Background(), state, previousRevision); err != nil {
		t.Fatal(err)
	}
}

func TestTSK574WorkerRestartReresolvesExistingAttachment(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
	logPath := installTSK574Airelay(t, fixture, "idle", true)
	beforeSessions := tsk574SessionIDs(t, fixture)
	resolved, err := fixture.server.Service.ResolveProjectWorker(context.Background(), fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}

	restarted := service.NewWithDurabilityDeferredWorkers(fixture.server.Service.Config, fixture.server.Service.Durability)
	restarted.Airelay.Timeout = time.Second
	after, err := restarted.ResolveProjectWorker(context.Background(), fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Agent.AgentID != resolved.Agent.AgentID || after.Binding.SessionKey != resolved.Binding.SessionKey || after.Session.ID != resolved.Session.ID {
		t.Fatalf("restart changed Worker attachment: before=%#v after=%#v", resolved, after)
	}
	if got := tsk574SessionIDs(t, fixture); strings.Join(got, "\n") != strings.Join(beforeSessions, "\n") {
		t.Fatalf("restart changed durable Sessions: before=%v after=%v", beforeSessions, got)
	}
	invocations, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(invocations), "start") {
		t.Fatalf("restart spawned a new Airelay process: %q", invocations)
	}
}

func TestTSK574WorkerReusesSessionAcrossReworkAndSequentialTasks(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
	logPath := installTSK574Airelay(t, fixture, "idle", true)
	beforeSessions := tsk574SessionIDs(t, fixture)
	first := tsk574Dispatch(t, fixture, fixture.task.ID)

	if _, err := fixture.server.Service.TaskExecutionRework(context.Background(), service.TaskExecutionReworkInput{
		ProjectID: fixture.projectID,
		Key:       fixture.task.ID,
		Stage:     "code",
		Comment:   "rework without rotating the Worker",
	}); err != nil {
		t.Fatal(err)
	}
	current := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/current", map[string]any{})
	if current["ok"] != true {
		t.Fatalf("Worker current after rework failed: %#v", current)
	}
	currentResult, ok := current["result"].(map[string]any)
	if !ok || currentResult["key"] != fixture.task.ID {
		t.Fatalf("unexpected current Task after rework: %#v", current)
	}
	tsk574MarkDone(t, fixture, fixture.task.ID)
	secondTask := tsk574CreateTask(t, fixture, "tsk574-sequential-task")
	second := tsk574Dispatch(t, fixture, secondTask.ID)
	if first.Agent != second.Agent {
		t.Fatalf("sequential Tasks changed logical Worker Agent: first=%#v second=%#v", first, second)
	}
	if got := tsk574SessionIDs(t, fixture); strings.Join(got, "\n") != strings.Join(beforeSessions, "\n") {
		t.Fatalf("Task switch changed durable Sessions: before=%v after=%v", beforeSessions, got)
	}
	invocations, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(invocations), "start") {
		t.Fatalf("Task switch spawned a new Airelay process: %q", invocations)
	}
}

func TestTSK574StalePreviousLaneCannotSubmitCurrentTask(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
	installTSK574Airelay(t, fixture, "idle", true)
	first := tsk574Dispatch(t, fixture, fixture.task.ID)
	tsk574MarkDone(t, fixture, fixture.task.ID)
	secondTask := tsk574CreateTask(t, fixture, "tsk574-stale-lane-task")
	tsk574Dispatch(t, fixture, secondTask.ID)
	lanePath, err := gitx.TaskWorktreePath(fixture.server.Service.Config.StateDir, fixture.projectID, secondTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lanePath); err != nil {
		t.Fatal(err)
	}
	staleBranch := "stale/previous-lane"
	testutil.Git(t, lanePath, "checkout", "-b", staleBranch)
	defer func() { testutil.Git(t, lanePath, "checkout", secondBranch(t, fixture, secondTask.ID)) }()

	rejected := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/submit-code", map[string]any{"cwd": first.Worktree})
	message := tsk571ErrorMessage(t, rejected)
	if !strings.Contains(message, "unknown argument \"cwd\"") && !strings.Contains(message, "unknown property \"cwd\"") {
		t.Fatalf("caller lane/cwd injection was not rejected: %q", message)
	}
	rejected = fixture.call(t, fixture.sessions[durableSession.RoleWorker], "task/submit-code", map[string]any{})
	message = tsk571ErrorMessage(t, rejected)
	if !strings.Contains(message, "server-owned branch") {
		t.Fatalf("stale previous lane rejection=%q", message)
	}
	state, found, err := fixture.server.Service.Durability.ReadTaskExecutionState(context.Background(), fixture.projectID, secondTask.ID)
	if err != nil || !found || state.Status != model.TaskExecutionDispatched {
		t.Fatalf("stale lane submission changed Task state: state=%#v found=%v err=%v", state, found, err)
	}
}

func secondBranch(t *testing.T, fixture *tsk571HTTPFixture, taskID string) string {
	t.Helper()
	state, found, err := fixture.server.Service.Durability.ReadTaskExecutionState(context.Background(), fixture.projectID, taskID)
	if err != nil || !found {
		t.Fatalf("read Task state for branch: %#v %v %v", state, found, err)
	}
	return state.Branch
}

func TestTSK574WorkerRejectsWrongProject(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
	installTSK574Airelay(t, fixture, "idle", true)
	ctx := service.WithAgentSessionID(context.Background(), fixture.sessions[durableSession.RoleWorker])
	if _, err := fixture.server.Service.TaskExecutionCurrent(ctx, "other"); err == nil || !strings.Contains(err.Error(), "RUNTIME_SESSION_UNAVAILABLE") {
		t.Fatalf("wrong-project Worker resolution error=%v", err)
	}
}

func TestTSK574WorkerBindingFailuresFailClosed(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	t.Run("dead runtime", func(t *testing.T) {
		fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
		installTSK574Airelay(t, fixture, "dead", false)
		if _, err := fixture.server.Service.ResolveProjectWorker(context.Background(), fixture.projectID); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
			t.Fatalf("dead Worker binding error=%v", err)
		}
	})
	t.Run("mismatched runtime", func(t *testing.T) {
		fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
		installTSK574Airelay(t, fixture, "idle", true)
		fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID][fixture.agentID] = config.AgentBinding{SessionKey: "different-runtime"}
		if _, err := fixture.server.Service.ResolveProjectWorker(context.Background(), fixture.projectID); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_UNAVAILABLE") {
			t.Fatalf("mismatched Worker binding error=%v", err)
		}
	})
	t.Run("multiple Worker bindings", func(t *testing.T) {
		fixture := newTSK571HTTPFixture(t, []string{durableSession.RoleWorker}, true, true)
		installTSK574Airelay(t, fixture, "idle", true)
		revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = seedTSK571Agent(t, fixture.server.Service, revision, "coding-secondary", true)
		fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID]["coding-secondary"] = config.AgentBinding{SessionKey: "runtime-secondary"}
		fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, "runtime-secondary")
		if _, err := fixture.server.Service.ResolveProjectWorker(context.Background(), fixture.projectID); err == nil || !strings.Contains(err.Error(), "RUNTIME_IDENTITY_AMBIGUOUS") {
			t.Fatalf("multiple Worker binding error=%v", err)
		}
	})
}

func TestTSK574SharedAgentRolesDoNotInterfereWithWorkerStatusReadiness(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleWorker}, true, true)
	installTSK574Airelay(t, fixture, "idle", true)
	project := fixture.server.Service.Config.Projects[fixture.projectID]
	project.ProjectCode = "EXM"
	fixture.server.Service.Config.Projects[fixture.projectID] = project

	worker, err := fixture.server.Service.ResolveProjectWorker(context.Background(), fixture.projectID)
	if err != nil {
		t.Fatal(err)
	}
	lead, err := fixture.server.Service.ResolveRuntimeRoleSession(context.Background(), fixture.runtime, durableSession.RoleLead)
	if err != nil {
		t.Fatal(err)
	}
	if worker.Agent.AgentID != fixture.agentID || worker.Session.ID != fixture.sessions[durableSession.RoleWorker] || lead.Session.ID != fixture.sessions[durableSession.RoleLead] || worker.Session.ID == lead.Session.ID {
		t.Fatalf("role binding interference: worker=%#v lead=%#v", worker, lead)
	}

	projectStatus := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "project/status", map[string]any{})
	if projectStatus["ok"] != true {
		t.Fatalf("project/status failed: %#v", projectStatus)
	}
	projectResult := projectStatus["result"].(map[string]any)
	projectAgent := projectResult["agent"].(map[string]any)
	agentStatus := fixture.call(t, fixture.sessions[durableSession.RolePlanner], "agent/status", map[string]any{"key": fixture.agentID})
	if agentStatus["ok"] != true {
		t.Fatalf("agent/status failed: %#v", agentStatus)
	}
	agentResult := agentStatus["result"].(map[string]any)
	if projectAgent["session_ready"] != true || projectAgent["state"] != "idle" || agentResult["status"] != "idle" {
		t.Fatalf("readiness parity mismatch: project=%#v agent=%#v", projectAgent, agentResult)
	}

	installTSK574Airelay(t, fixture, "running", true)
	projectStatus = fixture.call(t, fixture.sessions[durableSession.RolePlanner], "project/status", map[string]any{})
	if projectStatus["ok"] != true {
		t.Fatalf("busy project/status failed: %#v", projectStatus)
	}
	projectResult = projectStatus["result"].(map[string]any)
	projectAgent = projectResult["agent"].(map[string]any)
	agentStatus = fixture.call(t, fixture.sessions[durableSession.RolePlanner], "agent/status", map[string]any{"key": fixture.agentID})
	if agentStatus["ok"] != true {
		t.Fatalf("busy agent/status failed: %#v", agentStatus)
	}
	agentResult = agentStatus["result"].(map[string]any)
	if projectAgent["session_ready"] != true || projectAgent["task_id"] != nil || agentResult["task"] != nil || agentResult["status"] != "busy" {
		t.Fatalf("busy transcript was projected as canonical Task work: project=%#v agent=%#v", projectAgent, agentResult)
	}
}
