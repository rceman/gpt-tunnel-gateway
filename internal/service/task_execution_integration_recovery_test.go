package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func seedTaskIntegrationRecovery(t *testing.T, status string, executionRevision int, captureRevision int, integrationHead string) (*Service, context.Context, model.TaskExecutionState, string) {
	t.Helper()
	s, _, _ := testService(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	task, _, err := s.taskAuthoringCreateShared(context.Background(), "tsk521-recovery-task", TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Recovery task",
		Summary:     "Recovery summary.",
		Objective:   "Recover integration safely.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.taskLifecycleComplete(context.Background(), task.ProjectID, task.ID, "gateway", "integration complete"); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	state := model.TaskExecutionState{
		TaskID: task.ID, ProjectID: task.ProjectID, Status: status, Stage: "code", Worktree: taskExecutionWorktree(task.ID, head[:8]),
		BaseHead: head, Head: head, Branch: "task/" + task.ID + "-recovery", TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256,
		Agent: "coder-example", ExecutionRevision: executionRevision, UpdatedAt: time.Now().UTC(),
	}
	if err := db.CreateTaskExecutionState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	opID := "mutation-" + strings.Repeat("b", 64)
	capture := taskExecutionIntegrationCapture{
		SchemaVersion:      2,
		ProjectID:          task.ProjectID,
		TaskID:             task.ID,
		TaskRevision:       task.Revision,
		TaskRevisionSHA256: task.RevisionSHA256,
		ExecutionRevision:  captureRevision,
		BaseHead:           head,
		LaneHead:           head,
		Branch:             state.Branch,
		CandidateHead:      head,
		CandidateTree:      head,
		IntegrationHead:    integrationHead,
		Gates:              []model.CompletionGateResult{{ID: "integration", ExitCode: 0}},
	}
	raw, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeDurableMutation(durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   opID,
		Kind:          "task-integrate",
		RequestSHA256: strings.Repeat("c", 64),
		ProjectID:     task.ProjectID,
		Input:         json.RawMessage(`{"key":"EXM-TSK1"}`),
		Status:        "completed",
		CapturedState: string(raw),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return s, withDurableMutationOperationID(context.Background(), opID), state, opID
}

func TestTSK521PostMainRecoveryCompletesAfterTaskDone(t *testing.T) {
	s, ctx, want, _ := seedTaskIntegrationRecovery(t, model.TaskExecutionIntegrating, 1, 1, strings.Repeat("d", 40))
	got, found, err := s.readExecutionForIntegration(ctx, want.ProjectID, want.TaskID)
	if err != nil || !found || got != want {
		t.Fatalf("recovery state=%#v found=%v err=%v, want %#v", got, found, err, want)
	}
}

func TestTSK521CompletedIntegrationRetryIsIdempotent(t *testing.T) {
	s, ctx, want, _ := seedTaskIntegrationRecovery(t, model.TaskExecutionIntegrated, 2, 1, strings.Repeat("d", 40))
	first, found, err := s.readExecutionForIntegration(ctx, want.ProjectID, want.TaskID)
	if err != nil || !found {
		t.Fatalf("first recovery read=%#v found=%v err=%v", first, found, err)
	}
	second, found, err := s.readExecutionForIntegration(ctx, want.ProjectID, want.TaskID)
	if err != nil || !found || second != first {
		t.Fatalf("repeated recovery read=%#v found=%v err=%v first=%#v", second, found, err, first)
	}
}

func TestTSK521PartialOrMismatchedIntegrationCaptureFailsClosed(t *testing.T) {
	s, ctx, want, opID := seedTaskIntegrationRecovery(t, model.TaskExecutionIntegrated, 2, 1, strings.Repeat("d", 40))
	operation, err := s.readDurableMutation(opID)
	if err != nil {
		t.Fatal(err)
	}
	var capture taskExecutionIntegrationCapture
	if err := json.Unmarshal([]byte(operation.CapturedState), &capture); err != nil {
		t.Fatal(err)
	}
	capture.TaskID = "EXM-TSK999"
	raw, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	operation.CapturedState = string(raw)
	if err := s.writeDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.readExecutionForIntegration(ctx, want.ProjectID, want.TaskID); err == nil || found {
		t.Fatalf("mismatched capture accepted: found=%v err=%v", found, err)
	}
}
