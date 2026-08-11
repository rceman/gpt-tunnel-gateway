package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func createTrainTask(t *testing.T, s *Service, revision, slug string) (model.Task, string) {
	t.Helper()
	task, operation, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Title:              "Train task " + slug,
		Objective:          "Exercise one explicit ordered train task.",
		Slug:               slug,
		AcceptanceCriteria: []string{"bounded"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task, operation.Hub.After
}

func TestTaskTrainCreateBindsOnlyExplicitFirstTaskAndStatusIsBounded(t *testing.T) {
	s, revision, _ := testService(t)
	first, revision := createTrainTask(t, s, revision, "first")
	second, revision := createTrainTask(t, s, revision, "second")
	train, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
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
	if operation.Hub.After == "" || train.CurrentTaskID != first.ID || train.CurrentIndex != 0 || train.CurrentRunID != "" {
		t.Fatalf("unexpected train creation: %#v %#v", train, operation)
	}
	status, err := s.TaskTrainStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.CurrentTaskID != first.ID || status.NextTaskID != second.ID || status.TaskCount != 2 || status.CurrentTaskState != "created" {
		t.Fatalf("unexpected bounded train status: %#v", status)
	}
	if _, _, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID, first.ID},
		CreatedBy: "planner",
	}); err == nil {
		t.Fatal("duplicate train creation unexpectedly succeeded")
	}
}

func TestTaskTrainNamedLanesDispatchIndependentlyAndExposeIdentity(t *testing.T) {
	s, revision, baseRevision := testService(t)
	first, revision := createTrainTask(t, s, revision, "lane-first")
	second, revision := createTrainTask(t, s, revision, "lane-second")
	firstTrain, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID:    "example",
		TrainID:      "train-first",
		TaskIDs:      []string{first.ID},
		BaseRevision: baseRevision,
		LaneBranch:   "train/first",
		CreatedBy:    "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstTrain.TrainID != "train-first" || firstTrain.LaneBranch != "train/first" || firstTrain.BaseRevision != baseRevision {
		t.Fatalf("first lane identity was not persisted: %#v", firstTrain)
	}
	secondTrain, _, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID:    "example",
		TrainID:      "train-second",
		TaskIDs:      []string{second.ID},
		BaseRevision: baseRevision,
		LaneBranch:   "train/second",
		CreatedBy:    "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example", TrainID: secondTrain.TrainID})
	if err != nil {
		t.Fatal(err)
	}
	if status.TrainID != secondTrain.TrainID || status.CurrentRunID == "" {
		t.Fatalf("named lane did not dispatch: %#v", status)
	}
	run, err := s.RunRead(context.Background(), status.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.TrainID != secondTrain.TrainID || run.LaneBranch != secondTrain.LaneBranch {
		t.Fatalf("run lost lane identity: %#v", run)
	}
	if _, err := s.TaskTrainStatus(context.Background(), "example"); err == nil {
		t.Fatal("ambiguous project-level task train status unexpectedly succeeded")
	}
	byID, err := s.TaskTrainStatusByID(context.Background(), "example", firstTrain.TrainID)
	if err != nil || byID.TrainID != firstTrain.TrainID {
		t.Fatalf("named task train status lookup failed: %#v err=%v", byID, err)
	}
}

func TestTaskTrainPollWaitsForDeliveryAfterTaskCompletion(t *testing.T) {
	s, revision, _ := testService(t)
	first, revision := createTrainTask(t, s, revision, "delivery-wait")
	second, revision := createTrainTask(t, s, revision, "delivery-next")
	train, operation, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
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
	completed := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: first.ID, TaskSHA256: first.SHA256, Status: "completed", UpdatedAt: time.Now().UTC()}
	_, err = s.Hub.Transact(context.Background(), operation.Hub.After, "test: complete train task", func(worktree string) ([]string, error) {
		path := s.taskStatePath("example", first.ID)
		if err := hub.WriteJSON(worktree, path, completed); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.TaskTrainPoll(context.Background(), TaskTrainPollInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != model.TaskTrainWaitingDelivery || status.WaitReason != "delivery_review_or_merge_required" || status.NextTaskID != second.ID {
		t.Fatalf("unexpected delivery wait status: %#v", status)
	}
	got, err := s.TaskTrainRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskTrainWaitingDelivery || got.CurrentIndex != train.CurrentIndex {
		t.Fatalf("unexpected persisted delivery wait: %#v", got)
	}
}

func TestTaskTrainActiveRunResumesCompactionAndRepromptsExplicitStallOnce(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/train-supervision")
	writeLivenessScript(t, s, "Context compaction completed\nAcknowledged; resuming", "Controller: reachable\nState: idle", "record")
	status, err := s.taskTrainTail(context.Background(), TaskTrainStatus{
		ProjectID:    run.ProjectID,
		CurrentRunID: run.ID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.WaitReason != "compaction_resume_sent" || status.AgentState != model.AgentStateCompactedResuming {
		t.Fatalf("compaction was not supervised: %#v", status)
	}
	writeLivenessScript(t, s, "Execution stalled; no new output", "Controller: reachable\nState: error", "record")
	status, err = s.taskTrainTail(context.Background(), TaskTrainStatus{
		ProjectID:    run.ProjectID,
		CurrentRunID: run.ID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if status.WaitReason != "execution_stall_reprompt_sent" {
		t.Fatalf("explicit stall was not reprompted: %#v", status)
	}
	updated, err := s.RunRead(context.Background(), run.ID)
	if err != nil || updated.RepromptCount != 1 || updated.LastRepromptAt == nil {
		t.Fatalf("reprompt reservation was not durable: %#v err=%v", updated, err)
	}
	_, err = s.taskTrainTail(context.Background(), TaskTrainStatus{
		ProjectID:    run.ProjectID,
		CurrentRunID: run.ID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(filepath.Dir(s.Airelay.Command), "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(calls), "prompt"); got != 1 {
		t.Fatalf("stall supervision sent duplicate prompt: count=%d calls=%q", got, calls)
	}
}

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
