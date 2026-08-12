package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func writeTrainMergedState(t *testing.T, s *Service, revision string, task model.Task, projectHead string) string {
	t.Helper()
	state := model.TaskState{
		SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256,
		Status: "merged", ReviewedHead: projectHead, IntegrationBranch: "main", IntegrationHead: projectHead,
		UpdatedAt: time.Now().UTC(),
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: mark train task merged", func(worktree string) ([]string, error) {
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(worktree, path, state); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx.After
}

func TestTaskTrainPollMergedDispatchesNextAtCurrentHeadAndCompletes(t *testing.T) {
	s, revision, projectHead := testService(t)
	first, revision := createTrainTask(t, s, revision, "merged-first")
	second, revision := createTrainTask(t, s, revision, "merged-second")
	_, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID, second.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision = writeTrainMergedState(t, s, operation.Hub.After, first, projectHead)
	status, err := s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentTaskID != second.ID || status.CurrentRunID == "" || status.CurrentIndex != 1 {
		t.Fatalf("merged task did not advance: %#v", status)
	}
	run, err := s.RunRead(context.Background(), status.CurrentRunID)
	if err != nil || run.TaskID != second.ID || run.BaseRevision != projectHead {
		t.Fatalf("next run did not use current default head: %#v err=%v", run, err)
	}
	runsBefore, err := s.RunList(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example"}); err != nil {
		t.Fatal(err)
	}
	runsAfter, err := s.RunList(context.Background(), "example")
	if err != nil || len(runsAfter) != len(runsBefore) {
		t.Fatalf("repeated poll created a duplicate run: before=%d after=%d err=%v", len(runsBefore), len(runsAfter), err)
	}
	finishRevision := mustHubRevision(t, s)
	secondRun, err := s.RunRead(context.Background(), status.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	secondRun.Status, secondRun.FinishedAt = "succeeded", &finished
	secondState := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: second.ID, TaskSHA256: second.SHA256, Status: "merged", ReviewedHead: projectHead, IntegrationBranch: "main", IntegrationHead: projectHead, UpdatedAt: finished}
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	plan.ActiveRunID = ""
	plan.UpdatedAt = finished
	plan.Revision++
	_, err = s.Hub.Transact(context.Background(), finishRevision, "test: finish final train task", func(worktree string) ([]string, error) {
		statePath := s.taskStatePath("example", second.ID)
		runPath := s.runPath("example", secondRun.ID)
		if err := hub.WriteJSON(worktree, statePath, secondState); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, runPath, secondRun); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.planPath("example"), plan); err != nil {
			return nil, err
		}
		return []string{statePath, runPath, s.planPath("example")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err = s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example"})
	if err != nil || status.Status != model.TaskTrainCompleted || status.CurrentIndex != 2 {
		t.Fatalf("final merged task did not complete train: %#v err=%v", status, err)
	}
}

func TestTaskTrainPollStopsCancelledSupersededAndDeferredTasks(t *testing.T) {
	for _, taskStatus := range []string{"cancelled", "superseded", "deferred"} {
		t.Run(taskStatus, func(t *testing.T) {
			s, revision, _ := testService(t)
			task, revision := createTrainTask(t, s, revision, "blocked-"+taskStatus)
			train, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
				ProjectID: "example",
				TaskIDs:   []string{task.ID},
				CreatedBy: "planner",
				WriteOptions: WriteOptions{
					ExpectedHubRevision: revision,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStatus, UpdatedAt: time.Now().UTC()}
			if taskStatus == "deferred" {
				state.ReviewedHead = strings.Repeat("a", 40)
				state.DeferredReason = "delivery decision"
			}
			_, err = s.Hub.Transact(context.Background(), operation.Hub.After, "test: block train task", func(worktree string) ([]string, error) {
				path := s.taskStatePath("example", task.ID)
				if err := hub.WriteJSON(worktree, path, state); err != nil {
					return nil, err
				}
				return []string{path}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			status, err := s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example"})
			if err != nil || status.Status != model.TaskTrainBlocked || status.CurrentTaskID != train.CurrentTaskID {
				t.Fatalf("task status did not stop train: %#v err=%v", status, err)
			}
		})
	}
}
