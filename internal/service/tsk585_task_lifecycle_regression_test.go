package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func tsk585CompleteTask(t *testing.T, s *Service, idem, title string, criteria ...string) model.TaskAuthoring {
	t.Helper()
	task, _, err := s.taskAuthoringCreateShared(context.Background(), idem, TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              title,
		Summary:            "Completion summary.",
		Objective:          "Complete task safely.",
		AcceptanceCriteria: criteria,
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func tsk585AcceptanceFact(t *testing.T, task model.TaskAuthoring, criterion int, mode, integrationHead, deliverable string) string {
	t.Helper()
	fact := taskCompletionAcceptanceFact{
		SchemaVersion:      1,
		TaskRevisionSHA256: task.RevisionSHA256,
		Criterion:          criterion,
		Decision:           "accept",
		Mode:               mode,
	}
	if integrationHead != "" {
		fact.IntegrationHead = &integrationHead
	}
	if deliverable != "" {
		fact.DeliverableKind = &deliverable
	}
	raw, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	return taskCompletionAcceptanceFactPrefix + string(raw)
}
func tsk585RawAcceptanceFact(t *testing.T, task model.TaskAuthoring, criterion int, mode string, head *string, deliverable *string) string {
	t.Helper()
	raw, err := json.Marshal(taskCompletionAcceptanceFact{
		SchemaVersion:      1,
		TaskRevisionSHA256: task.RevisionSHA256,
		Criterion:          criterion,
		Decision:           "accept",
		Mode:               mode,
		IntegrationHead:    head,
		DeliverableKind:    deliverable,
	})
	if err != nil {
		t.Fatal(err)
	}
	return taskCompletionAcceptanceFactPrefix + string(raw)
}
func tsk585CompleteEvidence(t *testing.T, s *Service, task model.TaskAuthoring, sessionID *string, kind model.OperatorJournalKind, facts []string, commits []string) model.OperatorJournalEvent {
	t.Helper()
	event, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		SessionID:  sessionID,
		Kind:       kind,
		Summary:    "acceptance evidence",
		Content:    model.OperatorJournalContent{Facts: facts},
		References: model.OperatorJournalReferences{Tasks: []string{task.ID}, Commits: commits},
		Actor:      "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func tsk585CompletionInput(task model.TaskAuthoring, mode, reason string, evidence map[int][]string) TaskCompleteInput {
	in := TaskCompleteInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      mode,
		Reason:    reason,
	}
	for i := 1; i <= len(task.AcceptanceCriteria); i++ {
		in.Acceptance = append(in.Acceptance, TaskCompleteAcceptanceInput{
			Criterion: i,
			Evidence:  evidence[i],
		})
	}
	return in
}
func tsk585IntegratedCompleteFixture(t *testing.T, s *Service, key string) (model.TaskAuthoring, string) {
	t.Helper()
	for attempt := 0; attempt < 3; attempt++ {
		idem := key
		if attempt > 0 {
			idem = fmt.Sprintf("%s-r%d", key, attempt)
		}
		task := tsk585CompleteTask(t, s, idem, "Completable", "criterion one")
		tsk585Dispatch(t, s, task.ID)
		tsk585LaneCommit(t, s, task.ID, "candidate")
		tsk585DriveToVerified(t, s, task.ID)
		operation := tsk585Integrate(t, s, task.ID)
		if operation.Status != "completed" {
			t.Fatalf("integrate status=%q error=%q", operation.Status, operation.Error)
		}
		ctx := context.Background()
		receipt, receiptFound, err := s.Durability.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
		phases, phaseErr := s.Durability.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
		if err != nil || phaseErr != nil || !receiptFound || len(phases) != 1 {
			t.Fatalf("integrated fixture durable proof incomplete: receiptFound=%v err=%v phases=%#v phaseErr=%v", receiptFound, err, phases, phaseErr)
		}
		if receipt.CompletedAt.After(phases[0].CreatedAt) {
			// A transient backward wall-clock step inverted the durable
			// verification/integration ordering; the poisoned evidence is
			// immutable, so rebuild the fixture with a fresh Task identity.
			continue
		}
		capture := tsk585IntegrateCapture(t, s, operation.OperationID)
		return task, capture.IntegrationHead
	}
	t.Fatal("wall clock repeatedly inverted the verification/integration ordering")
	return model.TaskAuthoring{}, ""
}
func tsk585CompletionEvent(t *testing.T, db *sqlitestore.Databases, key string) sqlitestore.TaskLifecycleEvent {
	t.Helper()
	event, found, err := db.ReadTaskCompletionEvent(context.Background(), "example", key)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("missing completion lifecycle event")
	}
	return event
}
func tsk585CompletionOutbox(t *testing.T, db *sqlitestore.Databases, key string) int {
	t.Helper()
	rows, err := db.Shared.Query(context.Background(), `SELECT id FROM hub_outbox WHERE entity_id=? AND kind='task-complete'`, key)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows.Rows)
}
func TestTSK585TaskCompleteIntegrated(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk585-complete-int")
	stateBefore, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	receipt, receiptFound, err := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if err != nil || !receiptFound {
		t.Fatalf("integrated fixture requires a verification receipt: %v", err)
	}
	historyBefore, err := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := tsk585PlannerSession(t, s)
	event := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "integrated", integration, "")}, []string{integration})
	in := tsk585CompletionInput(task, "integrated", "all criteria accepted", map[int][]string{1: {event.ID}})
	out, err := s.TaskComplete(ctx, in, "planner")
	if err != nil {
		stateDiag, stateFound, stateErr := db.ReadTaskExecutionState(ctx, "example", task.ID)
		receiptDiag, receiptFound, receiptErr := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
		var codePhases, testsPhases, rebasePhases, integrationPhases []sqlitestore.TaskExecutionPhase
		var codeErr, testsErr, rebaseErr, integrationErr error
		codePhases, codeErr = db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
		testsPhases, testsErr = db.ReadTaskExecutionPhases(ctx, "example", task.ID, "tests")
		rebasePhases, rebaseErr = db.ReadTaskExecutionPhases(ctx, "example", task.ID, "rebase")
		integrationPhases, integrationErr = db.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
		currentReceipt, currentOK, _, currentErr := s.taskExecutionVerificationProofCurrent(ctx, stateDiag)
		t.Fatalf("first completion: %v state=%#v stateFound=%v stateErr=%v receipt=%#v receiptFound=%v receiptErr=%v currentReceipt=%#v currentOK=%v currentErr=%v code=%#v codeErr=%v tests=%#v testsErr=%v rebase=%#v rebaseErr=%v integration=%#v integrationErr=%v", err, stateDiag, stateFound, stateErr, receiptDiag, receiptFound, receiptErr, currentReceipt, currentOK, currentErr, codePhases, codeErr, testsPhases, testsErr, rebasePhases, rebaseErr, integrationPhases, integrationErr)
	}
	if out != (TaskCompleteOutput{
		Key:      task.ID,
		Status:   "done",
		Revision: task.Revision,
	}) {
		t.Fatalf("output=%#v", out)
	}
	entity, _ := db.ReadSharedTask(ctx, task.ID)
	var done model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &done); err != nil {
		t.Fatal(err)
	}
	if done.Status != model.TaskAuthoringDone || done.Revision != task.Revision || done.RevisionSHA256 != task.RevisionSHA256 || done.ReadySeal != nil {
		t.Fatalf("task=%#v", done)
	}
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state.Status != model.TaskExecutionDone || state.ExecutionRevision != stateBefore.ExecutionRevision+1 || state.Head != stateBefore.Head || state.Branch != stateBefore.Branch {
		t.Fatalf("execution=%#v", state)
	}
	lifecycle := tsk585CompletionEvent(t, db, task.ID)
	if lifecycle.Actor != "planner" || lifecycle.Reason != "all criteria accepted" || lifecycle.FromStatus != task.Status || lifecycle.ToStatus != "done" || lifecycle.EventKind != "complete" || lifecycle.Revision != int64(task.Revision) {
		t.Fatalf("lifecycle=%#v", lifecycle)
	}
	var contract taskCompletionContract
	if err := decodeStrict(lifecycle.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Mode != "integrated" || contract.IntegrationHead != integration || contract.TaskRevisionSHA256 != task.RevisionSHA256 || len(contract.Acceptance) != 1 ||
		contract.VerificationOperationID != receipt.OperationID || contract.VerificationAttemptRevision != receipt.AttemptRevision {
		t.Fatalf("contract=%#v", contract)
	}
	if got := tsk585CompletionOutbox(t, db, task.ID); got != 1 {
		t.Fatalf("outbox rows=%d", got)
	}
	historyAfter, err := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(historyAfter.Records, historyBefore.Records) {
		t.Fatal("completion must not alter content history")
	}
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lanePath); err != nil {
		t.Fatalf("lane worktree removed: %v", err)
	}
	again, err := s.TaskComplete(ctx, in, "other-actor")
	if err != nil {
		stateReplay, stateFound, stateErr := db.ReadTaskExecutionState(ctx, "example", task.ID)
		phasesReplay, phasesErr := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
		eventReplay, eventFound, eventErr := db.ReadTaskCompletionEvent(ctx, "example", task.ID)
		t.Fatalf("identical replay: %v done.UpdatedAt=%v state=%#v found=%v stateErr=%v phases=%#v phasesErr=%v event=%#v found=%v eventErr=%v", err, done.UpdatedAt, stateReplay, stateFound, stateErr, phasesReplay, phasesErr, eventReplay, eventFound, eventErr)
	}
	if again != out {
		t.Fatalf("replay output=%#v", again)
	}
	if got := tsk585CompletionOutbox(t, db, task.ID); got != 1 {
		t.Fatalf("replay must not add outbox rows: %d", got)
	}
	events, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	if len(events) != 1 {
		t.Fatalf("replay must not add lifecycle events: %v", events)
	}
	state2, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if state2 != state {
		t.Fatalf("replay must not advance execution: %#v", state2)
	}
	conflict := in
	conflict.Reason = "different"
	if _, err := s.TaskComplete(ctx, conflict, "planner"); err == nil {
		t.Fatal("conflicting reason must reject")
	}
	conflict = in
	conflict.Mode = "historical"
	if _, err := s.TaskComplete(ctx, conflict, "planner"); err == nil {
		t.Fatal("conflicting mode must reject")
	}
}
func TestTSK585TaskCompleteNonCode(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-complete-nc", "Non Code", "doc criterion", "second criterion")
	sessionID := tsk585PlannerSession(t, s)
	e1 := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "non_code", "", "non_code")}, nil)
	e2 := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 2, "non_code", "", "non_code")}, nil)
	out, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "non_code", "docs done", map[int][]string{1: {e1.ID}, 2: {e2.ID}}), "planner")
	if err != nil {
		t.Fatal(err)
	}
	if out != (TaskCompleteOutput{
		Key:      task.ID,
		Status:   "done",
		Revision: task.Revision,
	}) {
		t.Fatalf("output=%#v", out)
	}
	if _, found, _ := db.ReadTaskExecutionState(ctx, "example", task.ID); found {
		t.Fatal("non_code must never create execution state")
	}
	phases, _ := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "integration")
	if len(phases) != 0 {
		t.Fatalf("non_code must not write phases: %v", phases)
	}
	contract := taskCompletionContract{}
	decodeStrict(tsk585CompletionEvent(t, db, task.ID).Contract, &contract)
	if contract.IntegrationHead != "" || contract.Mode != "non_code" || contract.VerificationOperationID != "" || contract.VerificationAttemptRevision != 0 {
		t.Fatalf("contract=%#v", contract)
	}
}
func TestTSK585TaskCompleteHistorical(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-complete-hist", "Historical", "criterion")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerification(t, s, task.ID)
	project := s.Config.Projects["example"]
	base := tsk585MainHead(t, s)
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	laneTree := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD^{tree}"))
	integration := tsk585CommitTree(t, project.Root, laneTree, base, "historical landing")
	tsk585SetRemoteMain(t, s, integration)
	evidence := tsk585JournalEvidence(t, s, task.ID, []string{integration}, nil)
	operation := tsk585HistoricalIntegrate(t, s, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "legacy",
			Evidence:        evidence.ID,
		},
	})
	if operation.Status != "completed" {
		t.Fatalf("historical integrate: %q", operation.Error)
	}
	if _, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID); found {
		t.Fatal("fixture requires no verification receipt")
	}
	sessionID := tsk585PlannerSession(t, s)
	acc := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "historical", integration, "")}, []string{integration})
	out, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "historical", "recognized and accepted", map[int][]string{1: {acc.ID}}), "planner")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "done" {
		t.Fatalf("output=%#v", out)
	}
	if _, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID); found {
		t.Fatal("historical completion must not fabricate a verification receipt")
	}
	histContract := taskCompletionContract{}
	decodeStrict(tsk585CompletionEvent(t, db, task.ID).Contract, &histContract)
	if histContract.VerificationOperationID != "" || histContract.VerificationAttemptRevision != 0 {
		t.Fatalf("historical contract must not carry verification identity: %#v", histContract)
	}
}
func TestTSK585TaskCompleteEvidenceAuthority(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-complete-auth", "Authority", "one")
	fact := tsk585AcceptanceFact(t, task, 1, "non_code", "", "non_code")
	record := func(sessionID *string, actor string) string {
		t.Helper()
		return tsk585CompleteEvidence(t, s, task, sessionID, model.OperatorTaskReview, []string{fact}, nil).ID
	}
	store := durableSession.NewStoreWithDurability(s.Durability)
	agentSession, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RoleAgent, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := store.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.End(ended.ID); err != nil {
		t.Fatal(err)
	}
	cross, err := store.Create(durableSession.CreateInput{ProjectID: "other", ProjectCode: "ZZZ", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	bogusID := "SP-EXM-0000"
	cases := map[string]string{
		"missing session":     record(nil, "planner"),
		"nonexistent session": record(&bogusID, "planner"),
		"agent session spoof": record(&agentSession.ID, "planner"),
		"ended session":       record(&ended.ID, "planner"),
		"cross-project":       record(&cross.ID, "planner"),
		"wrong kind":          tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskPlan, []string{fact}, nil).ID,
		"no task ref":         "",
		"malformed fact":      tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{taskCompletionAcceptanceFactPrefix + "{bad"}, nil).ID,
		"reject decision": tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{taskCompletionAcceptanceFactPrefix + mustJSON(taskCompletionAcceptanceFact{
			SchemaVersion:      1,
			TaskRevisionSHA256: task.RevisionSHA256,
			Criterion:          1,
			Decision:           "reject",
			Mode:               "non_code",
			DeliverableKind:    ptrStr("non_code"),
		})}, nil).ID,
		"wrong criterion fact": tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 2, "non_code", "", "non_code")}, nil).ID,
		"wrong mode":           tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "integrated", "", "non_code")}, nil).ID,
		"wrong task digest": tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{taskCompletionAcceptanceFactPrefix + mustJSON(taskCompletionAcceptanceFact{
			SchemaVersion:      1,
			TaskRevisionSHA256: strings.Repeat("e", 64),
			Criterion:          1,
			Decision:           "accept",
			Mode:               "non_code",
			DeliverableKind:    ptrStr("non_code"),
		})}, nil).ID,
		"missing deliverable": tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{taskCompletionAcceptanceFactPrefix + mustJSON(taskCompletionAcceptanceFact{
			SchemaVersion:      1,
			TaskRevisionSHA256: task.RevisionSHA256,
			Criterion:          1,
			Decision:           "accept",
			Mode:               "non_code",
		})}, nil).ID,
	}
	otherTask := tsk585CompleteTask(t, s, "tsk585-complete-auth2", "Other", "one")
	cases["no task ref"] = tsk585CompleteEvidence(t, s, otherTask, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{fact}, nil).ID
	superseded := tsk585CompleteEvidence(t, s, task, ptr(tsk585PlannerSession(t, s)), model.OperatorTaskReview, []string{fact}, nil)
	supSession := tsk585PlannerSession(t, s)
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:         "example",
		SessionID:         &supSession,
		Kind:              model.OperatorCorrection,
		Summary:           "correction",
		SupersedesEventID: superseded.ID,
		Actor:             "planner",
		References:        model.OperatorJournalReferences{Tasks: []string{task.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	cases["superseded"] = superseded.ID
	postDatedID := "SP-EXM-ZZZZ"
	postDated := tsk585CompleteEvidence(t, s, task, &postDatedID, model.OperatorTaskReview, []string{fact}, nil)
	time.Sleep(2 * time.Millisecond)
	postStore := durableSession.Store{Durability: s.Durability, TypedIDGenerator: func(string) (string, error) { return postDatedID, nil }}
	if _, err := postStore.Create(durableSession.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT}); err != nil {
		t.Fatal(err)
	}
	cases["post-dated session"] = postDated.ID
	for name, evidenceID := range cases {
		in := tsk585CompletionInput(task, "non_code", "ok", map[int][]string{1: {evidenceID}})
		if _, err := s.TaskComplete(ctx, in, "planner"); err == nil {
			t.Fatalf("%s must reject", name)
		}
	}
	entity, _ := db.ReadSharedTask(ctx, task.ID)
	var after model.TaskAuthoring
	json.Unmarshal(entity.Payload, &after)
	if after.Status != model.TaskAuthoringPlanned {
		t.Fatal("failed acceptance mutated the task")
	}
}
func TestTSK585TaskCompleteAtomicFaultRollsBack(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk585-complete-fault")
	stateBefore, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	entityBefore, _ := db.ReadSharedTask(ctx, task.ID)
	histBefore, _ := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	outboxBefore, _ := db.Shared.Query(ctx, `SELECT id FROM hub_outbox WHERE entity_id=?`, task.ID)
	if _, err := db.Shared.Exec(ctx, `CREATE TRIGGER fail_task_completion_event BEFORE INSERT ON shared_task_lifecycle_events BEGIN SELECT RAISE(ABORT,'injected completion event failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	sessionID := tsk585PlannerSession(t, s)
	ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "integrated", integration, "")}, []string{integration})
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "integrated", "ok", map[int][]string{1: {ev.ID}}), "planner"); err == nil {
		t.Fatal("injected completion failure must fail")
	} else if !strings.Contains(err.Error(), "injected completion event failure") {
		t.Fatalf("failure must come from the injected commit boundary: %v", err)
	}
	entityAfter, _ := db.ReadSharedTask(ctx, task.ID)
	if string(entityAfter.Payload) != string(entityBefore.Payload) || entityAfter.Revision != entityBefore.Revision {
		t.Fatal("failed completion mutated the shared task")
	}
	stateAfter, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if stateAfter != stateBefore {
		t.Fatal("failed completion mutated execution")
	}
	histAfter, _ := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	if len(histAfter.Records) != len(histBefore.Records) {
		t.Fatal("failed completion wrote history")
	}
	events, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	if len(events) != 0 {
		t.Fatal("failed completion wrote lifecycle events")
	}
	outboxAfter, _ := db.Shared.Query(ctx, `SELECT id FROM hub_outbox WHERE entity_id=?`, task.ID)
	if len(outboxAfter.Rows) != len(outboxBefore.Rows) {
		t.Fatal("failed completion wrote outbox rows")
	}
}
func ptr(v string) *string { return &v }
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
func TestTSK585TaskCompleteHistoricalBootstrap(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-complete-hb", "Historical Bootstrap", "one")
	tsk585Dispatch(t, s, task.ID)
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	tsk585FileCommit(t, lanePath, "candidate-"+task.ID+".txt", "candidate\n", "candidate")
	candidate := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD"))
	candidateTree := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD^{tree}"))
	state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
	project := s.Config.Projects["example"]
	integration := tsk585CommitTree(t, project.Root, candidateTree, state.BaseHead, "landing")
	tsk585SetRemoteMain(t, s, integration)
	fact := tsk585HistoricalGatesFact(t, s, state, candidate, candidateTree, nil)
	evidence := tsk585JournalEvidence(t, s, task.ID, []string{integration, candidate, state.BaseHead}, []string{fact})
	if op := tsk585HistoricalIntegrate(t, s, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "bootstrap_full",
			Evidence:        evidence.ID,
			CandidateHead:   candidate,
			MainBase:        state.BaseHead,
		},
	}); op.Status != "completed" {
		t.Fatalf("historical integrate: %q", op.Error)
	}
	sessionID := tsk585PlannerSession(t, s)
	acc := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "historical", integration, "")}, []string{integration})
	out, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "historical", "ok", map[int][]string{1: {acc.ID}}), "planner")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "done" {
		t.Fatalf("output=%#v", out)
	}
	if _, found, _ := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID); found {
		t.Fatal("historical completion must not fabricate a verification receipt")
	}
}
func TestTSK585TaskCompleteStaleVerification(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk585-complete-stale")
	receipt, found, err := db.ReadLatestTaskExecutionVerification(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("fixture requires a verification receipt: %v", err)
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["gate_profile_sha256"] = strings.Repeat("f", 64)
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_verifications SET receipt_json=? WHERE project_id=? AND task_id=? AND operation_id=?`, string(mutated), "example", task.ID, receipt.OperationID); err != nil {
		t.Fatal(err)
	}
	sessionID := tsk585PlannerSession(t, s)
	ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "integrated", integration, "")}, []string{integration})
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "integrated", "ok", map[int][]string{1: {ev.ID}}), "planner"); err == nil {
		t.Fatal("completion must reject a noncurrent verification profile")
	}
	events, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	if len(events) != 0 || tsk585CompletionOutbox(t, db, task.ID) != 0 {
		t.Fatal("rejected completion must not write")
	}
}
func TestTSK585TaskCompleteCrossCriterionReuse(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-complete-xc", "Cross", "one", "two")
	sessionID := tsk585PlannerSession(t, s)
	both := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{
		tsk585AcceptanceFact(t, task, 1, "non_code", "", "non_code"),
		tsk585AcceptanceFact(t, task, 2, "non_code", "", "non_code"),
	}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "non_code", "ok", map[int][]string{1: {both.ID}, 2: {both.ID}}), "planner"); err != nil {
		t.Fatalf("one event covering both criteria must succeed: %v", err)
	}
	task2 := tsk585CompleteTask(t, s, "tsk585-complete-xc2", "Cross", "one", "two")
	only1 := tsk585CompleteEvidence(t, s, task2, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task2, 1, "non_code", "", "non_code")}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task2, "non_code", "ok", map[int][]string{1: {only1.ID}, 2: {only1.ID}}), "planner"); err == nil {
		t.Fatal("reusing a criterion-1-only event for criterion 2 must reject")
	}
}
func TestTSK585TaskHistoryMerged(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-hist-merge", "Merged", "one")
	if _, _, err := s.taskAuthoringUpdateShared(ctx, "tsk585-hist-merge-upd", TaskAuthoringUpdateInput{
		ProjectID:        "example",
		TaskID:           task.ID,
		ExpectedRevision: task.Revision,
		Title:            ptrStr("Merged updated"),
		Reason:           "revise",
		UpdatedBy:        "planner",
	}); err != nil {
		t.Fatal(err)
	}
	entity, _ := db.ReadSharedTask(ctx, task.ID)
	var current model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		t.Fatal(err)
	}
	sessionID := tsk585PlannerSession(t, s)
	ev := tsk585CompleteEvidence(t, s, current, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, current, 1, "non_code", "", "non_code")}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(current, "non_code", "done", map[int][]string{1: {ev.ID}}), "planner"); err != nil {
		t.Fatal(err)
	}
	page, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore || len(page.Records) != 3 {
		t.Fatalf("history=%#v", page.Records)
	}
	if page.Records[0].Revision != 1 || page.Records[1].Revision != 2 || page.Records[2].Revision != 2 {
		t.Fatalf("history revisions=%v", page.Records)
	}
	last := page.Records[2]
	if last.MutationKind != "complete" || last.Actor != "planner" || last.Reason != "done" || len(last.ChangedFields) != 1 || last.ChangedFields[0] != "status" {
		t.Fatalf("completion history row=%#v", last)
	}
	var contract taskCompletionContract
	if err := decodeStrict(last.Payload, &contract); err != nil {
		t.Fatal(err)
	}
	read, err := s.TaskLifecycleRead(ctx, "example", task.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if read.Title != "Merged updated" || read.Revision != 2 {
		t.Fatalf("task/read revision=%#v", read)
	}
}
func ptrStr(v string) *string { return &v }
func TestTSK585TaskCompleteIntegrationObjectProof(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	project := s.Config.Projects["example"]

	t.Run("wrong sole parent", func(t *testing.T) {
		task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk585-complete-obj1")
		state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
		wrong := tsk585CommitTree(t, project.Root, strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", integration+"^{tree}")), tsk585MainHead(t, s), "wrong parent landing")
		tsk585SetRemoteMain(t, s, wrong)
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_phases SET head_sha=? WHERE project_id=? AND task_id=? AND stage='integration'`, wrong, "example", task.ID); err != nil {
			t.Fatal(err)
		}
		sessionID := tsk585PlannerSession(t, s)
		ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "integrated", wrong, "")}, []string{wrong})
		if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "integrated", "ok", map[int][]string{1: {ev.ID}}), "planner"); err == nil {
			t.Fatal("wrong sole parent must reject")
		}
		entity, _ := db.ReadSharedTask(ctx, task.ID)
		var after model.TaskAuthoring
		json.Unmarshal(entity.Payload, &after)
		stateAfter, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
		events, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
		if after.Status == model.TaskAuthoringDone || stateAfter != state || len(events) != 0 || tsk585CompletionOutbox(t, db, task.ID) != 0 {
			t.Fatal("rejected completion must not write")
		}
		releaseExecutionDone(t, db, task.ID)
	})

	t.Run("wrong tree", func(t *testing.T) {
		task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk585-complete-obj2")
		state, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
		otherTree := tsk585EmptyTree(t, s)
		wrong := tsk585CommitTree(t, project.Root, otherTree, state.BaseHead, "wrong tree landing")
		tsk585SetRemoteMain(t, s, wrong)
		if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_phases SET head_sha=? WHERE project_id=? AND task_id=? AND stage='integration'`, wrong, "example", task.ID); err != nil {
			t.Fatal(err)
		}
		sessionID := tsk585PlannerSession(t, s)
		ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "integrated", wrong, "")}, []string{wrong})
		if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "integrated", "ok", map[int][]string{1: {ev.ID}}), "planner"); err == nil {
			t.Fatal("wrong candidate tree must reject")
		}
		_ = integration
		entity, _ := db.ReadSharedTask(ctx, task.ID)
		var after model.TaskAuthoring
		json.Unmarshal(entity.Payload, &after)
		stateAfter, _, _ := db.ReadTaskExecutionState(ctx, "example", task.ID)
		events, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
		if after.Status == model.TaskAuthoringDone || stateAfter != state || len(events) != 0 || tsk585CompletionOutbox(t, db, task.ID) != 0 {
			t.Fatal("rejected completion must not write")
		}
		releaseExecutionDone(t, db, task.ID)
	})
}
func releaseExecutionDone(t *testing.T, db *sqlitestore.Databases, key string) {
	t.Helper()
	ctx := context.Background()
	state, _, err := db.ReadTaskExecutionState(ctx, "example", key)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = model.TaskExecutionDone
	state.ExecutionRevision++
	if err := db.UpdateTaskExecutionState(ctx, state, state.ExecutionRevision-1); err != nil {
		t.Fatal(err)
	}
}
func TestTSK585TaskHistoryServicePageBoundary(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-hist-bound", "Boundary", "one")
	entity, _ := db.ReadSharedTask(ctx, task.ID)
	var current model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &current); err != nil {
		t.Fatal(err)
	}
	hist0, err := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 4)
	if err != nil || len(hist0.Records) != 1 {
		t.Fatal("fixture requires the create history row")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, hist0.Records[0].RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	base := createdAt.Add(time.Second)
	payload := entity.Payload
	fields, _ := json.Marshal([]string{"title"})
	for rev := int64(2); rev <= 257; rev++ {
		recorded := base.Add(time.Duration(rev) * time.Second).Format(time.RFC3339Nano)
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES('task',?,?,?,'update','planner','revise',?,?,?)`, task.ID, "example", rev, fields, payload, recorded); err != nil {
			t.Fatal(err)
		}
	}
	lifecycleAt := base.Add(time.Duration(255)*time.Second + time.Millisecond*500)
	contract := []byte(`{"schema_version":1,"mode":"non_code","reason":"ok","task_revision":1,"task_revision_sha256":"` + current.RevisionSHA256 + `","acceptance":[{"criterion":1,"evidence":["EXM-JRN1"]}]}`)
	opSum := sha256.Sum256(append([]byte("example"+string(rune(0))+task.ID+string(rune(0))), contract...))
	opID := "task-complete-" + hex.EncodeToString(opSum[:])
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_lifecycle_events(operation_id,project_id,task_id,revision,event_kind,from_status,to_status,actor,reason,contract,recorded_at) VALUES(?,?,?,?,'complete','planned','done','planner','done',?,?)`, opID, "example", task.ID, int64(1), contract, lifecycleAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	first, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != sqlitestore.SharedLifecycleQueryMaxRows || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page records=%d hasMore=%v cursor=%q", len(first.Records), first.HasMore, first.NextCursor)
	}
	again, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil || again.NextCursor != first.NextCursor {
		t.Fatal("continuation cursor must be deterministic")
	}
	publicCursor := pagination.EncodeOpaqueKeyset("task-history:example:"+task.ID, first.NextCursor)
	if publicCursor == "" {
		t.Fatal("opaque cursor must encode")
	}
	second, err := s.TaskLifecycleHistory(ctx, "example", task.ID, publicCursor)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Records) != 2 {
		t.Fatalf("second page records=%v hasMore=%v", second.Records, second.HasMore)
	}
	seen := append(first.Records, second.Records...)
	if len(seen) != 258 {
		t.Fatalf("total=%d want 258", len(seen))
	}
	expected := map[string]bool{"c1": true}
	for rev := int64(2); rev <= 257; rev++ {
		expected[fmt.Sprintf("c%d", rev)] = true
	}
	expected["l1"] = true
	got := map[string]int{}
	for _, rec := range seen {
		key := "c" + fmt.Sprint(rec.Revision)
		if rec.MutationKind == "complete" {
			key = "l1"
		}
		got[key]++
	}
	for key := range expected {
		if got[key] != 1 {
			t.Fatalf("history row %s seen %d times", key, got[key])
		}
	}
	type tuple struct {
		at     string
		source int
		id     int64
	}
	visited := map[tuple]bool{}
	var lastTime time.Time
	var lastSource, lastID = -1, int64(-1)
	lifecycleCount := 0
	for _, rec := range seen {
		at, err := time.Parse(time.RFC3339Nano, rec.RecordedAt)
		if err != nil {
			t.Fatal(err)
		}
		source, id := 0, rec.Revision
		if rec.MutationKind == "complete" {
			source, id = 1, 1
			lifecycleCount++
		}
		key := tuple{rec.RecordedAt, source, id}
		if visited[key] {
			t.Fatalf("duplicate history tuple %#v", key)
		}
		visited[key] = true
		if at.Before(lastTime) || (at.Equal(lastTime) && (source < lastSource || (source == lastSource && id <= lastID))) {
			t.Fatalf("history out of order at %#v", rec)
		}
		lastTime, lastSource, lastID = at, source, id
	}
	if lifecycleCount != 1 {
		t.Fatalf("lifecycle rows=%d", lifecycleCount)
	}
	if second.Records[len(second.Records)-1].Revision != 257 {
		t.Fatal("repeated revision must be retained on the second page")
	}
	if _, err := s.TaskLifecycleHistory(ctx, "example", task.ID, publicCursor+"tampered"); err == nil {
		t.Fatal("tampered opaque cursor must reject")
	}
	wrongScope := pagination.EncodeOpaqueKeyset("task-history:example:EXM-TSK9999", first.NextCursor)
	if _, err := s.TaskLifecycleHistory(ctx, "example", task.ID, wrongScope); err == nil {
		t.Fatal("wrong-scoped opaque cursor must reject")
	}
}
func TestTSK585TaskCompleteFactPropertyPresence(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	empty := ""
	attempt := func(t *testing.T, task model.TaskAuthoring, mode, head string, factHead *string, factDeliverable *string, commits []string, wantErr bool) {
		t.Helper()
		sessionID := tsk585PlannerSession(t, s)
		ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585RawAcceptanceFact(t, task, 1, mode, factHead, factDeliverable)}, commits)
		_, err := s.TaskComplete(ctx, tsk585CompletionInput(task, mode, "ok", map[int][]string{1: {ev.ID}}), "planner")
		if wantErr && err == nil {
			t.Fatalf("head=%v deliverable=%v must reject", factHead, factDeliverable)
		}
		if !wantErr && err != nil {
			t.Fatalf("valid fact must pass: %v", err)
		}
	}
	t.Run("integrated", func(t *testing.T) {
		task, integration := tsk585IntegratedCompleteFixture(t, s, "tsk585-complete-fp1")
		h := integration
		nk := "non_code"
		attempt(t, task, "integrated", integration, nil, nil, []string{integration}, true)
		attempt(t, task, "integrated", integration, &empty, nil, []string{integration}, true)
		attempt(t, task, "integrated", integration, &h, &empty, []string{integration}, true)
		attempt(t, task, "integrated", integration, &h, &nk, []string{integration}, true)
		attempt(t, task, "integrated", integration, &h, nil, nil, true)
		attempt(t, task, "integrated", integration, &h, nil, []string{integration}, false)
		releaseExecutionDone(t, db, task.ID)
	})
	t.Run("historical", func(t *testing.T) {
		task, integration := tsk585HistoricalCompleteFixture(t, s, db, "tsk585-complete-fp2")
		h := integration
		attempt(t, task, "historical", integration, nil, nil, []string{integration}, true)
		attempt(t, task, "historical", integration, &h, nil, []string{integration}, false)
		releaseExecutionDone(t, db, task.ID)
	})
	t.Run("non_code", func(t *testing.T) {
		task := tsk585CompleteTask(t, s, "tsk585-complete-fp3", "Presence", "one")
		head := strings.Repeat("a", 40)
		nk := "non_code"
		sessionID := tsk585PlannerSession(t, s)
		for _, tc := range []struct {
			head, deliverable *string
			wantErr           bool
		}{
			{nil, nil, true},
			{nil, &empty, true},
			{&empty, &nk, true},
			{&head, &nk, true},
			{nil, &nk, false},
		} {
			ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585RawAcceptanceFact(t, task, 1, "non_code", tc.head, tc.deliverable)}, nil)
			_, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "non_code", "ok", map[int][]string{1: {ev.ID}}), "planner")
			if tc.wantErr && err == nil {
				t.Fatalf("head=%v deliverable=%v must reject", tc.head, tc.deliverable)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("valid fact must pass: %v", err)
			}
		}
	})
}
func tsk585HistoricalCompleteFixture(t *testing.T, s *Service, db *sqlitestore.Databases, idem string) (model.TaskAuthoring, string) {
	t.Helper()
	task := tsk585CompleteTask(t, s, idem, "Historical", "one")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	tsk585DriveToVerification(t, s, task.ID)
	project := s.Config.Projects["example"]
	base := tsk585MainHead(t, s)
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	laneTree := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", "HEAD^{tree}"))
	integration := tsk585CommitTree(t, project.Root, laneTree, base, "historical landing")
	tsk585SetRemoteMain(t, s, integration)
	evidence := tsk585JournalEvidence(t, s, task.ID, []string{integration}, nil)
	operation := tsk585HistoricalIntegrate(t, s, TaskExecutionIntegrateInput{
		ProjectID: "example",
		Key:       task.ID,
		Mode:      "historical",
		Historical: &TaskExecutionHistoricalIntegrationInput{
			IntegrationHead: integration,
			Profile:         "legacy",
			Evidence:        evidence.ID,
		},
	})
	if operation.Status != "completed" {
		t.Fatalf("historical integrate: %q", operation.Error)
	}
	return task, integration
}
func TestTSK585TaskHistoryChangedFields(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()

	planned := tsk585CompleteTask(t, s, "tsk585-hist-cf1", "Planned", "one")
	sessionID := tsk585PlannerSession(t, s)
	ev := tsk585CompleteEvidence(t, s, planned, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, planned, 1, "non_code", "", "non_code")}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(planned, "non_code", "ok", map[int][]string{1: {ev.ID}}), "planner"); err != nil {
		t.Fatal(err)
	}
	page, err := s.TaskLifecycleHistory(ctx, "example", planned.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 2 || !reflect.DeepEqual(page.Records[1].ChangedFields, []string{"status"}) {
		t.Fatalf("planned completion changed_fields=%v", page.Records)
	}

	ready := tsk585CompleteTask(t, s, "tsk585-hist-cf2", "Ready", "one")
	entity, _ := db.ReadSharedTask(ctx, ready.ID)
	var row model.TaskAuthoring
	json.Unmarshal(entity.Payload, &row)
	row.Status = model.TaskAuthoringReady
	row.ReadySeal = &model.TaskReadySeal{Revision: row.Revision, RevisionSHA256: row.RevisionSHA256, ReadyBy: "planner", ReadyAt: time.Now().UTC()}
	row.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(row)
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_tasks SET payload=?,updated_at=? WHERE id=? AND revision=?`, payload, row.UpdatedAt.UTC().Format(time.RFC3339Nano), ready.ID, entity.Revision); err != nil {
		t.Fatal(err)
	}
	ev2 := tsk585CompleteEvidence(t, s, row, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, row, 1, "non_code", "", "non_code")}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(row, "non_code", "ok", map[int][]string{1: {ev2.ID}}), "planner"); err != nil {
		t.Fatal(err)
	}
	page2, err := s.TaskLifecycleHistory(ctx, "example", ready.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Records) != 2 || !reflect.DeepEqual(page2.Records[1].ChangedFields, []string{"status", "ready_seal"}) || page2.Records[1].Revision != 1 {
		t.Fatalf("ready completion changed_fields=%v", page2.Records)
	}
}
func tsk585ArchiveSharedTask(t *testing.T, db *sqlitestore.Databases, task model.TaskAuthoring) sqlitestore.SharedTask {
	t.Helper()
	entity, err := db.ReadSharedTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}
func tsk585SetTaskStatus(t *testing.T, db *sqlitestore.Databases, task model.TaskAuthoring, status string) model.TaskAuthoring {
	t.Helper()
	ctx := context.Background()
	entity := tsk585ArchiveSharedTask(t, db, task)
	var row model.TaskAuthoring
	if err := json.Unmarshal(entity.Payload, &row); err != nil {
		t.Fatal(err)
	}
	row.Status = status
	row.ReadySeal = nil
	row.UpdatedAt = time.Now().UTC()
	if status == model.TaskAuthoringReady {
		row.ReadySeal = &model.TaskReadySeal{Revision: row.Revision, RevisionSHA256: row.RevisionSHA256, ReadyBy: "planner", ReadyAt: row.UpdatedAt}
	}
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_tasks SET payload=?,updated_at=? WHERE id=? AND revision=?`, payload, row.UpdatedAt.UTC().Format(time.RFC3339Nano), task.ID, entity.Revision); err != nil {
		t.Fatal(err)
	}
	return row
}
func tsk585ArchiveAssertions(t *testing.T, s *Service, db *sqlitestore.Databases, task model.TaskAuthoring, wantFrom string) {
	t.Helper()
	ctx := context.Background()
	historyBefore, err := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	entityBefore := tsk585ArchiveSharedTask(t, db, task)
	outboxBefore := tsk585LifecycleOutbox(t, db, task.ID, "task-archive")

	archived, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "retire")
	if err != nil {
		t.Fatal(err)
	}
	if archived.Revision != task.Revision || archived.RevisionSHA256 != task.RevisionSHA256 || archived.Status != model.TaskAuthoringArchived || archived.ReadySeal != nil || !archived.UpdatedAt.After(task.UpdatedAt) {
		t.Fatalf("archive=%#v", archived)
	}
	entityAfter := tsk585ArchiveSharedTask(t, db, task)
	if entityAfter.Revision != entityBefore.Revision {
		t.Fatalf("store revision changed: %d -> %d", entityBefore.Revision, entityAfter.Revision)
	}
	events, err := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	if err != nil || len(events) != 1 {
		t.Fatalf("lifecycle events=%v err=%v", events, err)
	}
	event := events[0]
	if event.EventKind != "archive" || event.FromStatus != wantFrom || event.ToStatus != model.TaskAuthoringArchived ||
		event.Actor != "planner" || event.Reason != "retire" || event.Revision != int64(task.Revision) ||
		!archived.UpdatedAt.Equal(event.RecordedAt) {
		t.Fatalf("archive event=%#v", event)
	}
	var contract taskArchiveContract
	if err := decodeStrict(event.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.SchemaVersion != 1 || contract.TaskRevision != task.Revision || contract.TaskRevisionSHA256 != task.RevisionSHA256 ||
		contract.FromStatus != wantFrom || contract.Reason != "retire" {
		t.Fatalf("archive contract=%#v", contract)
	}
	if tsk585LifecycleOutbox(t, db, task.ID, "task-archive") != outboxBefore+1 {
		t.Fatal("archive must publish exactly one outbox row")
	}
	historyAfter, err := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(historyAfter.Records, historyBefore.Records) {
		t.Fatal("archive must not write content history")
	}
	page, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	last := page.Records[len(page.Records)-1]
	wantFields := []string{"status"}
	if wantFrom == model.TaskAuthoringReady {
		wantFields = append(wantFields, "ready_seal")
	}
	if last.MutationKind != "archive" || !reflect.DeepEqual(last.ChangedFields, wantFields) || last.Revision != int64(task.Revision) {
		t.Fatalf("merged archive row=%#v", last)
	}
	content, err := s.TaskLifecycleRead(ctx, "example", task.ID, task.Revision)
	if err != nil || content.Title != task.Title || content.Status != model.TaskAuthoringPlanned ||
		content.ReadySeal != nil || content.Revision != task.Revision || content.RevisionSHA256 != task.RevisionSHA256 {
		t.Fatalf("immutable content read=%#v err=%v", content, err)
	}
	repeated, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "different reason")
	if err != nil || repeated.Revision != task.Revision {
		t.Fatalf("repeat archive=%#v err=%v", repeated, err)
	}
	eventsAgain, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	historyAgain, _ := db.ListSharedHistoryPage(ctx, "task", "example", task.ID, 0, 64)
	if len(eventsAgain) != 1 || tsk585LifecycleOutbox(t, db, task.ID, "task-archive") != outboxBefore+1 || len(historyAgain.Records) != len(historyAfter.Records) {
		t.Fatal("repeat archive must write nothing")
	}
}
func TestTSK585TaskArchiveStatusOnly(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	planned := tsk585CompleteTask(t, s, "tsk585-arch-planned", "Archive planned", "one")
	tsk585ArchiveAssertions(t, s, db, planned, model.TaskAuthoringPlanned)

	created := tsk585CompleteTask(t, s, "tsk585-arch-ready", "Archive ready", "one")
	ready := tsk585SetTaskStatus(t, db, created, model.TaskAuthoringReady)
	tsk585ArchiveAssertions(t, s, db, ready, model.TaskAuthoringReady)
}
func TestTSK585TaskArchiveAtomicRollback(t *testing.T) {
	for _, trigger := range []struct{ name, sql string }{
		{"lifecycle", `CREATE TRIGGER fail_task_archive_event BEFORE INSERT ON shared_task_lifecycle_events BEGIN SELECT RAISE(ABORT,'injected archive event failure'); END`},
		{"outbox", `CREATE TRIGGER fail_task_archive_outbox BEFORE INSERT ON hub_outbox BEGIN SELECT RAISE(ABORT,'injected archive outbox failure'); END`},
	} {
		t.Run(trigger.name, func(t *testing.T) {
			s, db := tsk585Setup(t)
			defer db.Close()
			ctx := context.Background()
			task := tsk585CompleteTask(t, s, "tsk585-arch-fault-"+trigger.name, "Archive fault", "one")
			before := tsk585ArchiveSharedTask(t, db, task)
			if _, err := db.Shared.Exec(ctx, trigger.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "retire"); err == nil || !strings.Contains(err.Error(), "injected archive") {
				t.Fatalf("archive must fail at the injected boundary: %v", err)
			}
			after := tsk585ArchiveSharedTask(t, db, task)
			if !reflect.DeepEqual(after, before) {
				t.Fatal("rolled-back archive must leave the Task unchanged")
			}
			events, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
			if len(events) != 0 || tsk585LifecycleOutbox(t, db, task.ID, "task-archive") != 0 {
				t.Fatal("rolled-back archive must leave no event or outbox")
			}
		})
	}
}
func TestTSK585TaskArchiveDone(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-arch-done", "Archive done", "one")
	sessionID := tsk585PlannerSession(t, s)
	ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "non_code", "", "non_code")}, nil)
	if _, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "non_code", "ok", map[int][]string{1: {ev.ID}}), "planner"); err != nil {
		t.Fatal(err)
	}
	completed, err := s.TaskLifecycleRead(ctx, "example", task.ID, 0)
	if err != nil || completed.Status != model.TaskAuthoringDone {
		t.Fatal(err)
	}
	archived, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "retire done")
	if err != nil {
		t.Fatal(err)
	}
	if archived.Revision != task.Revision || archived.RevisionSHA256 != task.RevisionSHA256 ||
		archived.Status != model.TaskAuthoringArchived || !archived.UpdatedAt.After(completed.UpdatedAt) {
		t.Fatalf("done archive=%#v", archived)
	}
	events, err := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	if err != nil || len(events) != 2 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	complete, archive := events[0], events[1]
	if complete.EventKind != "complete" || archive.EventKind != "archive" ||
		complete.ToStatus != model.TaskAuthoringDone || archive.FromStatus != model.TaskAuthoringDone || archive.ToStatus != model.TaskAuthoringArchived ||
		complete.Revision != int64(task.Revision) || archive.Revision != int64(task.Revision) ||
		!archive.RecordedAt.After(complete.RecordedAt) {
		t.Fatalf("events=%#v %#v", complete, archive)
	}
	page, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil || len(page.Records) != 3 {
		t.Fatalf("history=%#v err=%v", page, err)
	}
	if page.Records[0].MutationKind == "complete" || page.Records[1].MutationKind != "complete" || page.Records[2].MutationKind != "archive" {
		t.Fatalf("history kinds=%v", page.Records)
	}
	for _, rec := range page.Records {
		if rec.Revision != int64(task.Revision) {
			t.Fatalf("repeated revision=%v", page.Records)
		}
	}
	if !reflect.DeepEqual(page.Records[2].ChangedFields, []string{"status"}) {
		t.Fatalf("archive changed_fields=%v", page.Records[2].ChangedFields)
	}
	content, err := s.TaskLifecycleRead(ctx, "example", task.ID, task.Revision)
	if err != nil || content.Status != model.TaskAuthoringPlanned || content.RevisionSHA256 != task.RevisionSHA256 {
		t.Fatalf("immutable content=%#v err=%v", content, err)
	}
	for _, listed := range mustPage(t, s, "", "").Tasks {
		if listed.ID == task.ID {
			t.Fatal("archived Task leaked into default browse")
		}
	}
	for _, listed := range mustPage(t, s, "", model.TaskAuthoringDone).Tasks {
		if listed.ID == task.ID {
			t.Fatal("archived Task must not appear under status=done")
		}
	}
	archivedFound := false
	archivedPage := mustPage(t, s, "", model.TaskAuthoringArchived)
	for {
		for _, listed := range archivedPage.Tasks {
			if listed.ID == task.ID {
				archivedFound = true
			}
		}
		if !archivedPage.HasMore {
			break
		}
		archivedPage = mustPage(t, s, "", model.TaskAuthoringArchived, archivedPage.NextCursor)
	}
	if !archivedFound {
		t.Fatal("status=archived must return the archived Task")
	}
	includedFound := false
	pageIncl, err := s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for {
		for _, listed := range pageIncl.Tasks {
			if listed.ID == task.ID {
				includedFound = true
			}
		}
		if !pageIncl.HasMore {
			break
		}
		pageIncl, err = s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, pageIncl.NextCursor, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !includedFound {
		t.Fatal("include_archived must return the archived Task")
	}
	eventsBefore := len(events)
	outboxBefore := tsk585LifecycleOutbox(t, db, task.ID, "task-archive")
	if _, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "repeat"); err != nil {
		t.Fatal(err)
	}
	events2, _ := db.ListTaskLifecycleEvents(ctx, "example", task.ID, 256)
	page2, _ := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if len(events2) != eventsBefore || tsk585LifecycleOutbox(t, db, task.ID, "task-archive") != outboxBefore || len(page2.Records) != len(page.Records) {
		t.Fatal("repeat archive must write nothing")
	}
}
func TestTSK585TaskBrowseOmitsDone(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	visible := 0
	for i := 0; i < 300; i++ {
		task := tsk585CompleteTask(t, s, fmt.Sprintf("tsk585-browse-%04d", i), fmt.Sprintf("Browse %04d", i), "one")
		switch {
		case i%17 == 0:
			tsk585SetTaskStatus(t, db, task, model.TaskAuthoringDone)
		case i%13 == 5:
			tsk585SetTaskStatus(t, db, task, model.TaskAuthoringArchived)
		default:
			visible++
		}
	}
	if visible < sqlitestore.SharedLifecycleQueryMaxRows {
		t.Fatal("fixture must leave more than a page of visible rows")
	}
	var seen []string
	cursor := ""
	for {
		page, err := s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, cursor, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range page.Tasks {
			if task.Status == model.TaskAuthoringDone || task.Status == model.TaskAuthoringArchived {
				t.Fatalf("default browse returned %q: %s", task.Status, task.ID)
			}
			seen = append(seen, task.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != visible || len(uniqueStrings(seen)) != len(seen) {
		t.Fatalf("visible=%d seen=%d", visible, len(seen))
	}
	first, err := s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, "", false)
	if err != nil || len(first.Tasks) != sqlitestore.SharedLifecycleQueryMaxRows {
		t.Fatalf("first page=%d err=%v must be full of visible rows", len(first.Tasks), err)
	}
	page, err := s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var archivedCount, doneCount int
	cursor = ""
	for {
		for _, task := range page.Tasks {
			if task.Status == model.TaskAuthoringDone {
				doneCount++
			}
			if task.Status == model.TaskAuthoringArchived {
				archivedCount++
			}
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
		page, err = s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, cursor, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if doneCount != 0 || archivedCount == 0 {
		t.Fatalf("include_archived archived=%d done=%d", archivedCount, doneCount)
	}
	done, err := s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringDone, model.TaskTypeTask, "", false)
	if err != nil {
		t.Fatal(err)
	}
	doneIDs := map[string]bool{}
	for {
		for _, task := range done.Tasks {
			if task.Status != model.TaskAuthoringDone {
				t.Fatalf("status=done returned %q", task.Status)
			}
			doneIDs[task.ID] = true
		}
		if !done.HasMore {
			break
		}
		done, err = s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringDone, model.TaskTypeTask, done.NextCursor, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(doneIDs) == 0 {
		t.Fatal("status=done must return done Tasks")
	}
	archived, err := s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringArchived, model.TaskTypeTask, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for {
		for _, task := range archived.Tasks {
			if task.Status != model.TaskAuthoringArchived {
				t.Fatalf("status=archived returned %q", task.Status)
			}
		}
		if !archived.HasMore {
			break
		}
		archived, err = s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringArchived, model.TaskTypeTask, archived.NextCursor, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	filtered, err := s.TaskLifecycleListQuery(ctx, "example", "browse 0001", "", model.TaskTypeTask, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range filtered.Tasks {
		if task.Status == model.TaskAuthoringDone || task.Status == model.TaskAuthoringArchived {
			t.Fatalf("text filter returned hidden status %q", task.Status)
		}
	}
}
func uniqueStrings(values []string) map[string]bool {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	return seen
}
func tsk585LifecycleOutbox(t *testing.T, db *sqlitestore.Databases, key, kind string) int {
	t.Helper()
	rows, err := db.Shared.Query(context.Background(), `SELECT id FROM hub_outbox WHERE entity_id=? AND kind=?`, key, kind)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows.Rows)
}
func TestTSK585DoneTaskExactVisibility(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585CompleteTask(t, s, "tsk585-browse-done", "Completed browse target", "one")
	sessionID := tsk585PlannerSession(t, s)
	ev := tsk585CompleteEvidence(t, s, task, &sessionID, model.OperatorTaskReview, []string{tsk585AcceptanceFact(t, task, 1, "non_code", "", "non_code")}, nil)
	out, err := s.TaskComplete(ctx, tsk585CompletionInput(task, "non_code", "ok", map[int][]string{1: {ev.ID}}), "planner")
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.TaskLifecycleRead(ctx, "example", task.ID, 0)
	if err != nil || current.Status != model.TaskAuthoringDone || current.Revision != out.Revision || current.RevisionSHA256 != task.RevisionSHA256 {
		t.Fatalf("current done read=%#v err=%v", current, err)
	}
	history, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil || len(history.Records) != 2 || history.Records[0].Revision != 1 || history.Records[1].Revision != 1 || history.Records[1].MutationKind != "complete" {
		t.Fatalf("done history=%#v err=%v", history, err)
	}
	for _, page := range []TaskLifecyclePage{mustPage(t, s, "", ""), mustPage(t, s, "Completed browse", "")} {
		for _, listed := range page.Tasks {
			if listed.ID == task.ID {
				t.Fatalf("done Task %s leaked into default browse", task.ID)
			}
		}
	}
	done := mustPage(t, s, "", model.TaskAuthoringDone)
	found := false
	for {
		for _, listed := range done.Tasks {
			if listed.ID == task.ID {
				found = true
				if listed.Status != model.TaskAuthoringDone {
					t.Fatalf("done listing=%#v", listed)
				}
			}
		}
		if !done.HasMore {
			break
		}
		done = mustPage(t, s, "", model.TaskAuthoringDone, done.NextCursor)
	}
	if !found {
		t.Fatal("status=done must return the completed Task")
	}
}
func mustPage(t *testing.T, s *Service, text, status string, cursor ...string) TaskLifecyclePage {
	t.Helper()
	c := ""
	if len(cursor) == 1 {
		c = cursor[0]
	}
	page, err := s.TaskLifecycleListQuery(context.Background(), "example", text, status, model.TaskTypeTask, c, false)
	if err != nil {
		t.Fatal(err)
	}
	return page
}
func tsk585OutboxTask(t *testing.T, id, status string, updatedAt time.Time) model.TaskAuthoring {
	t.Helper()
	task, err := trainv2.NewTask("example", id, trainv2.AuthoringDraft{
		Title: "Outbox task", Summary: "Prove outbox ordering.", Objective: "Prove outbox ordering.", ADRRelation: model.TaskADRNoRequired,
	}, "planner", updatedAt.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	task.Status = status
	task.UpdatedAt = updatedAt
	if status == model.TaskAuthoringReady {
		task.ReadySeal = &model.TaskReadySeal{Revision: task.Revision, RevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", ReadyAt: updatedAt}
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	return task
}
func tsk585HubTask(t *testing.T, s *Service, taskID string) model.TaskAuthoring {
	t.Helper()
	var task model.TaskAuthoring
	if err := json.Unmarshal([]byte(testutil.Git(t, s.Config.Hub.RepositoryURL, "show", "main:"+s.taskAuthoringPath("example", taskID))), &task); err != nil {
		t.Fatal(err)
	}
	return task
}
func tsk585PublishTask(t *testing.T, s *Service, task model.TaskAuthoring) error {
	t.Helper()
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	return s.publishSharedTaskOutbox(context.Background(), sqlitestore.OutboxEntry{ID: "out-" + task.ID + "-" + task.UpdatedAt.Format("150405.000000000"), EntityType: "task", EntityID: task.ID, Revision: int64(task.Revision), Payload: payload})
}
func TestTSK585TaskOutboxOrdering(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	base := time.Now().UTC().Truncate(time.Second)

	task := tsk585OutboxTask(t, "EXM-TSK800", model.TaskAuthoringPlanned, base)
	ready := task
	ready.Status = model.TaskAuthoringReady
	ready.UpdatedAt = base.Add(time.Minute)
	ready.ReadySeal = &model.TaskReadySeal{Revision: ready.Revision, RevisionSHA256: ready.RevisionSHA256, ReadyBy: "planner", ReadyAt: ready.UpdatedAt}
	if err := tsk585PublishTask(t, s, ready); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, task); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("stale same-revision publication err=%v", err)
	}
	if got := tsk585HubTask(t, s, task.ID); got.Status != model.TaskAuthoringReady {
		t.Fatalf("Hub task=%q must remain ready", got.Status)
	}

	done := tsk585OutboxTask(t, "EXM-TSK801", model.TaskAuthoringDone, base.Add(2*time.Minute))
	planned := done
	planned.Status = model.TaskAuthoringPlanned
	planned.UpdatedAt = base
	if err := tsk585PublishTask(t, s, planned); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, done); err != nil {
		t.Fatalf("newer same-revision lifecycle publication must write: %v", err)
	}
	if got := tsk585HubTask(t, s, done.ID); got.Status != model.TaskAuthoringDone {
		t.Fatalf("Hub task=%q must be done", got.Status)
	}

	same := tsk585OutboxTask(t, "EXM-TSK802", model.TaskAuthoringPlanned, base)
	if err := tsk585PublishTask(t, s, same); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, same); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("equal publication err=%v", err)
	}

	contradict := tsk585OutboxTask(t, "EXM-TSK803", model.TaskAuthoringPlanned, base)
	if err := tsk585PublishTask(t, s, contradict); err != nil {
		t.Fatal(err)
	}
	conflict := contradict
	conflict.Status = model.TaskAuthoringDone
	if err := tsk585PublishTask(t, s, conflict); err == nil || errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("contradictory equal-time publication err=%v must be a hard error", err)
	}
	if got := tsk585HubTask(t, s, contradict.ID); got.Status != model.TaskAuthoringPlanned {
		t.Fatalf("Hub task must be unchanged: %#v", got)
	}

	digest := tsk585OutboxTask(t, "EXM-TSK804", model.TaskAuthoringPlanned, base)
	if err := tsk585PublishTask(t, s, digest); err != nil {
		t.Fatal(err)
	}
	digestConflict := digest
	digestConflict.Objective = "Different objective changes the digest"
	digestConflict.RevisionSHA256 = ""
	var err error
	if digestConflict.RevisionSHA256, err = model.HashTaskAuthoring(digestConflict); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, digestConflict); err == nil || errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("conflicting digest err=%v must be a hard error", err)
	}
	if got := tsk585HubTask(t, s, digest.ID); got.Objective != "Prove outbox ordering." {
		t.Fatalf("Hub task must be unchanged: %#v", got)
	}

	older := tsk585OutboxTask(t, "EXM-TSK805", model.TaskAuthoringPlanned, base)
	newer := older
	newer.Revision = 2
	newer.RevisionSHA256 = ""
	if newer.RevisionSHA256, err = model.HashTaskAuthoring(newer); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, newer); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, older); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("stale lower-revision publication err=%v", err)
	}
	if got := tsk585HubTask(t, s, older.ID); got.Revision != 2 {
		t.Fatalf("Hub task must remain revision 2: %#v", got)
	}
}
func tsk585HubTaskBytes(t *testing.T, s *Service, taskID string) []byte {
	t.Helper()
	return []byte(testutil.Git(t, s.Config.Hub.RepositoryURL, "show", "main:"+s.taskAuthoringPath("example", taskID)))
}
func TestTSK585TaskOutboxIdentity(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	base := time.Now().UTC().Truncate(time.Second)
	task := tsk585OutboxTask(t, "EXM-TSK820", model.TaskAuthoringPlanned, base)
	other := tsk585OutboxTask(t, "EXM-TSK821", model.TaskAuthoringPlanned, base)
	bare := s.Config.Hub.RepositoryURL
	work := t.TempDir()
	testutil.Git(t, "", "clone", bare, work)
	testutil.Git(t, work, "config", "user.email", "test@example.invalid")
	testutil.Git(t, work, "config", "user.name", "Test")
	path := s.taskAuthoringPath("example", task.ID)
	otherRaw, _ := json.Marshal(other)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(work, filepath.FromSlash(path))), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(path)), otherRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", path)
	testutil.Git(t, work, "commit", "-m", "foreign task")
	testutil.Git(t, work, "push", "origin", "main")
	if err := tsk585PublishTask(t, s, task); err == nil || errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("foreign Task identity err=%v must be a hard error", err)
	}
	if got := tsk585HubTaskBytes(t, s, task.ID); !bytes.Equal(got, otherRaw) {
		t.Fatal("foreign Hub task bytes must remain unchanged")
	}

	task2 := tsk585OutboxTask(t, "EXM-TSK822", model.TaskAuthoringPlanned, base)
	otherProject := task2
	otherProject.ProjectID = "other"
	otherProject.RevisionSHA256 = ""
	var err error
	if otherProject.RevisionSHA256, err = model.HashTaskAuthoring(otherProject); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateTaskAuthoring(otherProject); err != nil {
		t.Fatal(err)
	}
	otherRaw2, _ := json.Marshal(otherProject)
	path2 := s.taskAuthoringPath("example", task2.ID)
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(path2)), otherRaw2, 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, work, "add", path2)
	testutil.Git(t, work, "commit", "-m", "foreign project")
	testutil.Git(t, work, "push", "origin", "main")
	if err := tsk585PublishTask(t, s, task2); err == nil || errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("foreign project identity err=%v must be a hard error", err)
	}
	if got := tsk585HubTaskBytes(t, s, task2.ID); !bytes.Equal(got, otherRaw2) {
		t.Fatal("foreign Hub task bytes must remain unchanged")
	}

	payload, _ := json.Marshal(task2)
	for name, entry := range map[string]sqlitestore.OutboxEntry{
		"entity id":   {ID: "e1", EntityType: "task", EntityID: "EXM-TSK9999", Revision: int64(task2.Revision), Payload: payload},
		"entity type": {ID: "e2", EntityType: "adr", EntityID: task2.ID, Revision: int64(task2.Revision), Payload: payload},
		"revision":    {ID: "e3", EntityType: "task", EntityID: task2.ID, Revision: 99, Payload: payload},
	} {
		if err := s.publishSharedTaskOutbox(context.Background(), entry); err == nil || errors.Is(err, errSharedOutboxNoop) {
			t.Fatalf("%s identity mismatch must hard-fail", name)
		}
	}
	if got := tsk585HubTaskBytes(t, s, task2.ID); !bytes.Equal(got, otherRaw2) {
		t.Fatal("Hub task bytes must remain unchanged after rejected entries")
	}
}
func TestTSK585TaskArchiveOutboxCompose(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	base := time.Now().UTC().Truncate(time.Second)
	planned := tsk585OutboxTask(t, "EXM-TSK830", model.TaskAuthoringPlanned, base)
	done := planned
	done.Status = model.TaskAuthoringDone
	done.UpdatedAt = base.Add(time.Minute)
	archived := planned
	archived.Status = model.TaskAuthoringArchived
	archived.UpdatedAt = base.Add(2 * time.Minute)
	if err := tsk585PublishTask(t, s, planned); err != nil {
		t.Fatal(err)
	}
	if err := tsk585PublishTask(t, s, done); err != nil {
		t.Fatalf("newer done publication must write: %v", err)
	}
	if err := tsk585PublishTask(t, s, archived); err != nil {
		t.Fatalf("newer archive publication must write: %v", err)
	}
	if err := tsk585PublishTask(t, s, archived); !errors.Is(err, errSharedOutboxNoop) {
		t.Fatalf("equal archive publication err=%v must be terminal no-op", err)
	}
	for name, stale := range map[string]model.TaskAuthoring{"done": done, "planned": planned} {
		if err := tsk585PublishTask(t, s, stale); !errors.Is(err, errSharedOutboxNoop) {
			t.Fatalf("stale %s payload err=%v must be terminal no-op", name, err)
		}
	}
	if got := tsk585HubTask(t, s, archived.ID); got.Status != model.TaskAuthoringArchived {
		t.Fatalf("Hub task=%q must remain archived", got.Status)
	}
}
