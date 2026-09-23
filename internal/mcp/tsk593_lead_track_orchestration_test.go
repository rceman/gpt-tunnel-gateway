package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func tsk593PrepareFixture(t *testing.T) *tsk571HTTPFixture {
	t.Helper()
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleLead}, true, true)
	installTSK563Airelay(t, fixture)
	revision, err := fixture.server.Service.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	workerID, workerRuntime := "coding-tsk593-worker", "runtime-tsk593-worker"
	seedTSK571Agent(t, fixture.server.Service, revision, workerID, true)
	fixture.server.Service.Config.ProjectAgentBindings[fixture.projectID][workerID] = config.AgentBinding{SessionKey: workerRuntime}
	fixture.addSession(t, fixture.projectID, "EXM", durableSession.RoleWorker, workerRuntime)
	return fixture
}

func tsk593CreateTrack(t *testing.T, fixture *tsk571HTTPFixture, tasks []string, suffix string) model.Track {
	t.Helper()
	ctx := context.Background()
	milestone, _, err := fixture.server.Service.MilestoneLifecycleCreate(ctx, service.MilestoneCreateInput{
		ProjectID: fixture.projectID,
		Title:     "TSK593 " + suffix + " milestone",
		Tasks:     tasks,
		CreatedBy: "planner",
	}, "tsk593-"+suffix+"-milestone")
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := fixture.server.Service.TrackLifecycleCreate(ctx, service.TrackCreateInput{
		ProjectID: fixture.projectID,
		Milestone: milestone.ID,
		Title:     "TSK593 " + suffix + " Track",
		Tasks:     tasks,
		CreatedBy: "planner",
	}, "tsk593-"+suffix+"-track")
	if err != nil {
		t.Fatal(err)
	}
	return track
}

func tsk593CreateTask(t *testing.T, fixture *tsk571HTTPFixture, title, priority string, dependencies []string) model.TaskAuthoring {
	t.Helper()
	task, _, err := fixture.server.Service.TaskLifecycleCreate(context.Background(), service.TaskAuthoringCreateInput{
		ProjectID:    fixture.projectID,
		Title:        title,
		Summary:      title + " bounded summary",
		Objective:    title + " objective",
		Priority:     priority,
		Dependencies: dependencies,
		ADRRelation:  model.TaskADRNoRequired,
		CreatedBy:    "planner",
	}, "tsk593-task-"+strings.ToLower(strings.ReplaceAll(title, " ", "-")))
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func tsk593ActionResult(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	if response["ok"] != true {
		t.Fatalf("action failed: %#v", response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("action result is not an object: %#v", response)
	}
	return result
}

func tsk593PromptAndAwait(t *testing.T, fixture *tsk571HTTPFixture, workerID, taskID string) {
	t.Helper()
	lead := fixture.sessions[durableSession.RoleLead]
	if result := fixture.call(t, lead, "agent/prompt", map[string]any{"agent": workerID, "message": "Implement Task " + taskID + " from its canonical contract."}); result["ok"] != true {
		t.Fatalf("Lead could not supervise the bound Worker: %#v", result)
	}
	if result := fixture.call(t, lead, "agent/await", map[string]any{"agent": workerID, "seconds": 1}); result["ok"] != true {
		t.Fatalf("Lead could not observe completion of the Worker turn: %#v", result)
	}
}

func tsk593SetExecutionStatus(t *testing.T, fixture *tsk571HTTPFixture, taskID, status string) {
	t.Helper()
	ctx := context.Background()
	state, found, err := fixture.server.Service.Durability.ReadTaskExecutionState(ctx, fixture.projectID, taskID)
	if err != nil || !found {
		t.Fatalf("read Task execution state found=%v err=%v", found, err)
	}
	previousRevision := state.ExecutionRevision
	state.Status = status
	state.ExecutionRevision++
	state.UpdatedAt = time.Now().UTC()
	if err := fixture.server.Service.Durability.UpdateTaskExecutionState(ctx, state, previousRevision); err != nil {
		t.Fatal(err)
	}
}

func tsk593RestartGateway(t *testing.T, fixture *tsk571HTTPFixture) {
	t.Helper()
	restarted := service.NewWithDurabilityDeferredWorkers(fixture.server.Service.Config, fixture.server.Service.Durability)
	restarted.Airelay.Timeout = time.Second
	fixture.server.Service = restarted
}

func TestTSK593LeadRunsEligibleTrackMembersSequentiallyAndSubmitsDerivedReady(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := tsk593PrepareFixture(t)
	prerequisite := fixture.task
	dependent := tsk593CreateTask(t, fixture, "TSK593 dependent member", model.TaskPriorityP0, []string{prerequisite.ID})
	independent := tsk593CreateTask(t, fixture, "TSK593 independent member", model.TaskPriorityP1, nil)
	track := tsk593CreateTrack(t, fixture, []string{dependent.ID, prerequisite.ID, independent.ID}, "multi-task")
	lead := fixture.sessions[durableSession.RoleLead]

	view := tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": track.ID}))
	members, ok := view["tasks"].([]any)
	if !ok || len(members) != 3 {
		t.Fatalf("Track member projection=%#v", view["tasks"])
	}
	first, _ := members[0].(map[string]any)
	second, _ := members[1].(map[string]any)
	third, _ := members[2].(map[string]any)
	if first["key"] != dependent.ID || second["key"] != prerequisite.ID || third["key"] != independent.ID || first["priority"] != model.TaskPriorityP0 || third["priority"] != model.TaskPriorityP1 {
		t.Fatalf("Track order/priority projection=%#v", members)
	}
	dependencies, _ := first["dependencies"].([]any)
	if len(dependencies) != 1 || dependencies[0] != prerequisite.ID {
		t.Fatalf("dependency projection=%#v", first)
	}
	execution, _ := first["execution"].(map[string]any)
	if execution["status"] != model.TaskExecutionPlanned {
		t.Fatalf("planned execution projection=%#v", execution)
	}
	if _, exists := first["objective"]; exists {
		t.Fatalf("Track read duplicated a full Task body: %#v", first)
	}

	firstDispatch := tsk593ActionResult(t, fixture.call(t, lead, "task/dispatch", map[string]any{"key": independent.ID}))
	if firstDispatch["agent"] != "coding-tsk593-worker" {
		t.Fatalf("dispatch did not use the attached persistent Worker: %#v", firstDispatch)
	}
	tsk593PromptAndAwait(t, fixture, "coding-tsk593-worker", independent.ID)
	if blocked := fixture.call(t, lead, "task/dispatch", map[string]any{"key": prerequisite.ID}); blocked["ok"] == true || !strings.Contains(tsk571ErrorMessage(t, blocked), "already has an actionable Task") {
		t.Fatalf("second member dispatched before the first settled: %#v", blocked)
	}
	tsk593SetExecutionStatus(t, fixture, independent.ID, model.TaskExecutionIntegrated)
	secondDispatch := tsk593ActionResult(t, fixture.call(t, lead, "task/dispatch", map[string]any{"key": prerequisite.ID}))
	if secondDispatch["agent"] != firstDispatch["agent"] {
		t.Fatalf("sequential Tasks changed Worker identity: first=%#v second=%#v", firstDispatch, secondDispatch)
	}
	tsk593PromptAndAwait(t, fixture, "coding-tsk593-worker", prerequisite.ID)
	tsk593SetExecutionStatus(t, fixture, prerequisite.ID, model.TaskExecutionIntegrated)
	thirdDispatch := tsk593ActionResult(t, fixture.call(t, lead, "task/dispatch", map[string]any{"key": dependent.ID}))
	if thirdDispatch["agent"] != firstDispatch["agent"] {
		t.Fatalf("sequential Tasks changed Worker identity: first=%#v third=%#v", firstDispatch, thirdDispatch)
	}
	tsk593PromptAndAwait(t, fixture, "coding-tsk593-worker", dependent.ID)
	tsk593RestartGateway(t, fixture)

	reused := tsk593ActionResult(t, fixture.call(t, lead, "task/dispatch", map[string]any{"key": dependent.ID}))
	if reused["agent"] != thirdDispatch["agent"] || reused["worktree"] != thirdDispatch["worktree"] {
		t.Fatalf("restart did not reuse the existing Task execution: before=%#v after=%#v", thirdDispatch, reused)
	}
	resumed := tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": track.ID}))
	dispatched, _ := resumed["dispatched_tasks"].([]any)
	if len(dispatched) != 3 || dispatched[0] != independent.ID || dispatched[1] != prerequisite.ID || dispatched[2] != dependent.ID {
		t.Fatalf("restart lost or duplicated canonical Track dispatch history: %#v", resumed)
	}
	resumedMembers, _ := resumed["tasks"].([]any)
	resumedExecution, _ := resumedMembers[0].(map[string]any)["execution"].(map[string]any)
	if resumedExecution["status"] != model.TaskExecutionDispatched || resumedExecution["stage"] != "code" || resumedExecution["execution_revision"] != float64(1) {
		t.Fatalf("restart lost current Task execution state: %#v", resumedMembers[0])
	}

	tsk593SetExecutionStatus(t, fixture, dependent.ID, model.TaskExecutionIntegrated)
	ready := tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": track.ID}))
	if ready["status"] != model.TrackReady {
		t.Fatalf("Track readiness was not server-derived: %#v", ready)
	}
	submitted := tsk593ActionResult(t, fixture.call(t, lead, "track/submit", map[string]any{"key": track.ID}))
	if submitted["status"] != model.TrackReviewPending {
		t.Fatalf("Lead did not stop at Planner review boundary: %#v", submitted)
	}
}

func TestTSK593SemanticBlockerUsesDurableMSGAndResumesAfterRestart(t *testing.T) {
	t.Setenv("GPT_TUNNEL_SESSION", "")
	fixture := tsk593PrepareFixture(t)
	track := tsk593CreateTrack(t, fixture, []string{fixture.task.ID}, "blocker")
	planner := fixture.sessions[durableSession.RolePlanner]
	lead := fixture.sessions[durableSession.RoleLead]

	delegation := tsk593ActionResult(t, fixture.call(t, planner, "message/create", map[string]any{"to_role": durableSession.RoleLead, "body": track.ID}))
	assignmentID, _ := delegation["message"].(string)
	assignment := tsk593ActionResult(t, fixture.call(t, lead, "message/read", map[string]any{"message": assignmentID}))
	if assignment["body"] != track.ID {
		t.Fatalf("durable assignment did not preserve Track reference: %#v", assignment)
	}

	if result := fixture.call(t, lead, "task/dispatch", map[string]any{"key": fixture.task.ID}); result["ok"] != true {
		t.Fatalf("Lead could not dispatch blocker Task: %#v", result)
	}
	reason := "semantic decision required before changing the published API contract"
	if blocked := fixture.call(t, lead, "task/block", map[string]any{"key": fixture.task.ID, "reason": reason}); blocked["ok"] != true {
		t.Fatalf("Lead could not park the genuine semantic blocker: %#v", blocked)
	}
	body := "Track " + track.ID + "; Task " + fixture.task.ID + " is blocked. Decision required: confirm the existing API scope. Evidence: task/status reason=" + reason
	blocker := tsk593ActionResult(t, fixture.call(t, lead, "message/create", map[string]any{"to_role": durableSession.RolePlanner, "title": "Track semantic blocker", "body": body}))
	blockerID, _ := blocker["message"].(string)
	plannerRead := tsk593ActionResult(t, fixture.call(t, planner, "message/read", map[string]any{"message": blockerID}))
	if plannerRead["body"] != body || strings.Contains(body, fixture.task.Summary) {
		t.Fatalf("blocker MSG duplicated Task content or lost exact evidence: %#v", plannerRead)
	}
	if status := tsk593ActionResult(t, fixture.call(t, lead, "task/status", map[string]any{"key": fixture.task.ID})); status["status"] != model.TaskExecutionBlocked || status["reason"] != reason {
		t.Fatalf("canonical Task blocker state=%#v", status)
	}

	reply := tsk593ActionResult(t, fixture.call(t, planner, "message/create", map[string]any{"to_role": durableSession.RoleLead, "in_reply_to": blockerID, "body": "Keep the accepted scope unchanged; proceed using the existing contract."}))
	replyID, _ := reply["message"].(string)
	tsk593RestartGateway(t, fixture)
	resumedMessages := tsk593ActionResult(t, fixture.call(t, lead, "message/list", map[string]any{"unread_only": true}))
	items, _ := resumedMessages["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("restart lost Planner reply inbox state: %#v", resumedMessages)
	}
	readReply := tsk593ActionResult(t, fixture.call(t, lead, "message/read", map[string]any{"message": replyID}))
	if !strings.Contains(readReply["body"].(string), "accepted scope unchanged") {
		t.Fatalf("Planner reply was not durable after restart: %#v", readReply)
	}
	blockedStatus := tsk593ActionResult(t, fixture.call(t, lead, "task/status", map[string]any{"key": fixture.task.ID}))
	if blockedStatus["status"] != model.TaskExecutionBlocked || blockedStatus["reason"] != reason {
		t.Fatalf("restart changed canonical blocker state: %#v", blockedStatus)
	}
	resumed := tsk593ActionResult(t, fixture.call(t, lead, "task/resume", map[string]any{"key": fixture.task.ID, "reason": "Planner reply " + replyID + " confirms existing scope"}))
	if resumed["status"] != model.TaskExecutionDispatched {
		t.Fatalf("Lead did not resume the same canonical execution: %#v", resumed)
	}
	trackView := tsk593ActionResult(t, fixture.call(t, lead, "track/read", map[string]any{"key": track.ID}))
	if trackView["status"] != model.TrackActive || trackView["tasks"].([]any)[0].(map[string]any)["key"] != fixture.task.ID {
		t.Fatalf("Track authority or membership changed during blocker resume: %#v", trackView)
	}
}

func TestTSK593LeadActionBoundaryHasNoQueueOrRoleNamespace(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"task/dispatch", "task/review", "task/review_decide", "task/rework", "task/block", "task/resume", "task/test", "task/integrate", "agent/prompt", "agent/interrupt", "track/submit"} {
		if entries[path].AuthorityRole != durableSession.RoleLead {
			t.Fatalf("Lead execution authority for %s=%#v", path, entries[path])
		}
	}
	for _, path := range []string{"task/create", "task/update", "task/complete", "track/create", "track/update", "track/append_task", "track/remove_task", "track/cancel", "track/accept"} {
		if entries[path].AuthorityRole != durableSession.RolePlanner {
			t.Fatalf("Planner semantic authority for %s=%#v", path, entries[path])
		}
	}
	for path := range entries {
		if strings.HasPrefix(path, "lead/") || strings.HasPrefix(path, "worker/") || strings.Contains(path, "queue") || strings.Contains(path, "wave") || strings.Contains(path, "schedul") || path == "track/next" {
			t.Fatalf("duplicate Lead/Worker namespace or scheduler action registered: %s", path)
		}
	}
}
