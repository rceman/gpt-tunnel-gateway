package service

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectOperationalStatusFailsClosedOnTrainClassificationError(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	seedLiveTrainMutationForRetirementTest(t, s, "train-v2-future-mutation")
	result := ProjectOperationalStatus{
		State:                 "idle",
		RecommendedNextAction: "await work",
	}
	s.populateProjectOperationalTrain(&result, []model.TrainV2{staleTrainV2ForRetirementTest(time.Now().UTC())})
	if result.State != "blocked" || result.Blocker != "TRAIN_RECONCILIATION_UNAVAILABLE" || result.TrainID != "EXM-TRN1" {
		t.Fatalf("classification error was not projected as a blocker: %#v", result)
	}
}
func TestTrainV2RetirePreservesImmutableAttemptHistory(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	originalItems := append([]model.TrainV2Item(nil), train.Items...)
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed immutable history train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	retired, err := s.TrainV2Retire(trainV2RetirementTestContext(), TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "preserve immutable attempt history",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retired.Train.Items, originalItems) {
		t.Fatalf("retirement changed immutable TrainItem/Attempt history: before=%#v after=%#v", originalItems, retired.Train.Items)
	}
}
func TestTrainV2ReconcileTRN13RetiresWithoutResurrectingTSK272(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	train.ID = "GTW-TRN13"
	train.Items[0].TaskID = "GTW-TSK272"
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed failed historical TRN13", func(worktree string) ([]string, error) {
		trainPath := s.trainV2Path("example", train.ID)
		taskPath := s.taskAuthoringPath("example", "GTW-TSK272")
		statePath := s.taskStatePath("example", "GTW-TSK272")
		if err := hub.WriteJSON(worktree, trainPath, train); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, taskPath, map[string]any{"task_id": "GTW-TSK272", "status": "failed", "revision": 7}); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, statePath, map[string]any{"task_id": "GTW-TSK272", "status": "failed", "updated_at": time.Now().UTC()}); err != nil {
			return nil, err
		}
		return []string{trainPath, taskPath, statePath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	taskBefore, err := s.Hub.ReadFile(context.Background(), s.taskAuthoringPath("example", "GTW-TSK272"))
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := s.Hub.ReadFile(context.Background(), s.taskStatePath("example", "GTW-TSK272"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.TrainV2Reconcile(trainV2RetirementTestContext(), TrainV2ReconcileInput{
		ProjectID: "example",
		Apply:     true,
		Reason:    "terminalize failed historical Train",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil || result.Hub.Status != "reconciled" || len(result.Records) != 1 || result.Records[0].Status != model.TrainV2Retired {
		t.Fatalf("TRN13 was not terminalized: %#v err=%v", result, err)
	}
	current, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil || current.Status != model.TrainV2Retired || current.Items[0].TaskID != "GTW-TSK272" || len(current.Items[0].Attempts) != 1 {
		t.Fatalf("TRN13/TSK272 history was not preserved: %#v err=%v", current, err)
	}
	taskAfter, err := s.Hub.ReadFile(context.Background(), s.taskAuthoringPath("example", "GTW-TSK272"))
	if err != nil {
		t.Fatal(err)
	}
	stateAfter, err := s.Hub.ReadFile(context.Background(), s.taskStatePath("example", "GTW-TSK272"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(taskBefore, taskAfter) || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("TRN13 reconciliation changed durable TSK272 state: task_changed=%t state_changed=%t", !bytes.Equal(taskBefore, taskAfter), !bytes.Equal(stateBefore, stateAfter))
	}
}
