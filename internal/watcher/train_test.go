package watcher

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func watcherTrainFixture(t *testing.T) (model.TrainV2, model.TrainV2StartRecord, trainv2.RuntimeBinding) {
	t.Helper()
	now := time.Now().UTC()
	taskHash := strings.Repeat("a", 64)
	base := strings.Repeat("b", 40)
	train := model.TrainV2{SchemaVersion: 1, ID: "GTW-TRN7", ProjectID: "gateway", Revision: 1, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "GTW-TSK179", TaskRevision: 1, TaskRevisionSHA256: taskHash, Status: model.TrainV2ItemRunning, AddedAt: now, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "agent-1", AirelaySessionKey: "gateway_master", GatewayID: "gateway", StartHead: base, StartedAt: now}}, ActiveAttemptNumber: 1}}}
	start := model.TrainV2StartRecord{SchemaVersion: 1, ProjectID: "gateway", TrainID: train.ID, Status: model.TrainV2StartActive, IntegrationBranch: "main", BaseRevision: base, LaneBranch: "train/GTW-TRN7", CurrentItemPosition: 0, CurrentAttemptNumber: 1, CurrentTaskID: "GTW-TSK179", CurrentTaskRevision: 1, CurrentTaskRevisionSHA256: taskHash, StartedAt: now}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "gateway", TrainID: train.ID, WorktreePath: "/state/train-worktrees/gateway/GTW-TRN7", AgentID: "agent-1", SessionKey: "gateway_master", ItemPosition: 0, TaskID: "GTW-TSK179", AttemptNumber: 1, StartedAt: now}
	return train, start, runtime
}

func TestBindTrainAttemptRequiresExactIdentity(t *testing.T) {
	train, start, runtime := watcherTrainFixture(t)
	binding, err := BindTrainAttempt(train, start, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if binding.AttemptNumber != 1 || binding.TaskID != "GTW-TSK179" {
		t.Fatalf("unexpected binding: %#v", binding)
	}
	start.CurrentAttemptNumber = 2
	if _, err := BindTrainAttempt(train, start, runtime); err == nil {
		t.Fatal("wrong Attempt was accepted")
	}
}

func TestStartNextItemCreatesAttemptOneWithoutRunIdentity(t *testing.T) {
	train, _, runtime := watcherTrainFixture(t)
	finished := time.Now().UTC()
	train.Items[0].Status = model.TrainV2ItemFinalized
	train.Items[0].Attempts[0].Status = model.TrainV2AttemptSucceeded
	train.Items[0].Attempts[0].FinishedAt = &finished
	train.Items[0].SuccessfulAttemptNumber = 1
	train.Items[0].Proof = &model.TrainV2ImplementationProof{CheckpointHead: strings.Repeat("d", 40), ImplementationSHA: strings.Repeat("d", 40), ReportID: "report", GateResults: []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}, RecordedAt: finished}
	train.Status = model.TrainV2Running
	train.Items = append(train.Items, model.TrainV2Item{Position: 1, TaskID: "GTW-TSK180", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("c", 64), Status: model.TrainV2ItemQueued, AddedAt: time.Now().UTC()})
	plan, ok, err := PlanAutoAdvance(train, TrainBinding{
		TrainID:       train.ID,
		ItemPosition:  0,
		TaskID:        "GTW-TSK179",
		AttemptNumber: 1,
		AgentID:       runtime.AgentID,
		SessionKey:    runtime.SessionKey,
		WorktreePath:  runtime.WorktreePath,
		LaneBranch:    "train/GTW-TRN7",
	}, model.TrainV2AttemptSucceeded)
	if err != nil || !ok {
		t.Fatalf("advance was not planned: %v %v", ok, err)
	}
	updated, err := StartNextItem(train, plan, strings.Repeat("d", 40), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items[1].Attempts) != 1 || updated.Items[1].Attempts[0].Number != 1 {
		t.Fatalf("next item does not have Attempt 1: %#v", updated.Items[1])
	}
}
