package watcher

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func trainWatcherFixture(t *testing.T) (model.TrainV2, model.TrainV2StartRecord, trainv2.RuntimeBinding, model.Run) {
	t.Helper()
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	base := strings.Repeat("a", 40)
	taskHash := strings.Repeat("b", 64)
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion, ID: "GTW-TRN7", ProjectID: "gateway", Revision: 2, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
		Items: []model.TrainV2Item{
			{Position: 0, TaskID: "GTW-TSK179", TaskRevision: 1, TaskRevisionSHA256: taskHash, Status: model.TrainV2ItemRunning, AddedAt: now, RunID: "GTW-TSK179-RUN1", AgentID: "agent-1", StartHead: base},
			{Position: 1, TaskID: "GTW-TSK180", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("c", 64), Status: model.TrainV2ItemQueued, AddedAt: now},
		},
	}
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	start := model.TrainV2StartRecord{SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: "gateway", TrainID: train.ID, Status: model.TrainV2StartActive, IntegrationBranch: "main", BaseRevision: base, LaneBranch: "train/GTW-TRN7", RunID: "GTW-TSK179-RUN1", CurrentTaskID: "GTW-TSK179", CurrentTaskRevision: 1, CurrentTaskRevisionSHA256: taskHash, StartedAt: now}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "gateway", TrainID: train.ID, WorktreePath: "/state/train-worktrees/gateway/GTW-TRN7", AgentID: "agent-1", SessionKey: "gateway_master", RunID: start.RunID, StartedAt: now}
	run := model.Run{SchemaVersion: model.SchemaVersion, ID: start.RunID, TaskID: start.CurrentTaskID, TaskSHA256: taskHash, ProjectID: "gateway", SessionKey: runtime.SessionKey, AgentID: runtime.AgentID, Branch: start.LaneBranch, TrainID: train.ID, LaneBranch: start.LaneBranch, BaseRevision: base, Status: "dispatched", CompletionPath: "runs/GTW-TSK179-RUN1/completion.json", CreatedAt: now}
	if err := model.ValidateTrainV2StartRecord(start); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateRun(run); err != nil {
		t.Fatal(err)
	}
	return train, start, runtime, run
}

func TestTrainWatcherBindsSoleCurrentItemAndSession(t *testing.T) {
	train, start, runtime, run := trainWatcherFixture(t)
	binding, err := BindTrainRun(train, start, runtime, run)
	if err != nil {
		t.Fatal(err)
	}
	if binding.TrainID != train.ID || binding.TaskID != start.CurrentTaskID || binding.AgentID != runtime.AgentID || binding.SessionKey != runtime.SessionKey || binding.ItemPosition != 0 {
		t.Fatalf("unexpected Train watcher binding: %#v", binding)
	}
	run.SessionKey = "other_session"
	if _, err := BindTrainRun(train, start, runtime, run); err == nil {
		t.Fatal("watcher accepted a different session")
	}
}

func TestTrainWatcherRejectsRetiredExecutionGeneration(t *testing.T) {
	train, start, runtime, run := trainWatcherFixture(t)
	runtime.RestartRequired = true
	if _, err := BindTrainRun(train, start, runtime, run); err == nil {
		t.Fatal("watcher bound a retired Train execution generation")
	}
}

func TestTrainWatcherAutoAdvancesImmediateQueuedItemWithSameOwner(t *testing.T) {
	train, start, runtime, run := trainWatcherFixture(t)
	binding, err := BindTrainRun(train, start, runtime, run)
	if err != nil {
		t.Fatal(err)
	}
	now := start.StartedAt.Add(time.Minute)
	finalized, err := trainv2.RecordImplementationProof(train, binding.TaskID, binding.RunID, binding.AgentID, strings.Repeat("d", 40), strings.Repeat("e", 40), "GTW-TSK179-RUN1-REPORT", []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok, err := PlanAutoAdvance(finalized, binding, "succeeded")
	if err != nil || !ok || plan.Next.TaskID != "GTW-TSK180" || plan.SessionKey != runtime.SessionKey || plan.AgentID != runtime.AgentID {
		t.Fatalf("unexpected auto-advance plan: %#v %v %v", plan, ok, err)
	}
	advanced, err := StartNextItem(finalized, plan, "GTW-TSK180-RUN1", strings.Repeat("d", 40), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if advanced.Items[1].Status != model.TrainV2ItemRunning || advanced.Items[1].AgentID != runtime.AgentID || advanced.Items[1].RunID != "GTW-TSK180-RUN1" {
		t.Fatalf("next item was not started under same owner: %#v", advanced.Items[1])
	}
	if _, ok, err := PlanAutoAdvance(finalized, binding, "failed"); err != nil || ok {
		t.Fatalf("failed finalization advanced: ok=%v err=%v", ok, err)
	}
}

func TestTrainWatcherReviewsEarlierImmutableItemAfterLaneAdvance(t *testing.T) {
	train, start, runtime, run := trainWatcherFixture(t)
	binding, err := BindTrainRun(train, start, runtime, run)
	if err != nil {
		t.Fatal(err)
	}
	now := start.StartedAt.Add(time.Minute)
	finalized, err := trainv2.RecordImplementationProof(train, binding.TaskID, binding.RunID, binding.AgentID, strings.Repeat("d", 40), strings.Repeat("e", 40), "GTW-TSK179-RUN1-REPORT", []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}, now)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := trainv2.RecordReview(finalized, binding.TaskID, model.ReviewOutcomeAccepted, "GTW-TSK179-RUN1-REVIEW", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	item, err := ReviewableItem(reviewed, binding.TaskID)
	if err != nil || item.Proof == nil || item.Proof.CheckpointHead != strings.Repeat("d", 40) {
		t.Fatalf("earlier immutable item was not reviewable: %#v %v", item, err)
	}
}
