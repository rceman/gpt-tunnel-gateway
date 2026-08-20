package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2RetireRechecksLiveOperationInsideTransaction(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed atomic retirement train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	s.clock = func() time.Time {
		once.Do(func() { seedLiveTrainMutationForRetirementTest(t, s, "train-attempt-finalize") })
		return time.Now().UTC()
	}
	_, err = s.TrainV2Retire(trainV2RetirementTestContext(), TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "must recheck live operation",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "became active before retirement") {
		t.Fatalf("retirement did not fail closed on transaction-time live operation: %v", err)
	}
}
func TestTrainV2ReconcileRechecksLiveOperationInsideTransaction(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed atomic reconcile train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	s.clock = func() time.Time {
		once.Do(func() { seedLiveTrainMutationForRetirementTest(t, s, "train-attempt-review") })
		return time.Now().UTC()
	}
	_, err = s.TrainV2Reconcile(trainV2RetirementTestContext(), TrainV2ReconcileInput{
		ProjectID: "example",
		Apply:     true,
		Reason:    "must recheck live operation",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "became active before reconciliation") {
		t.Fatalf("reconciliation did not fail closed on transaction-time live operation: %v", err)
	}
}
func TestTrainV2StaticStaleClassificationDoesNotRetirePlannedOrIntegration(t *testing.T) {
	now := time.Now().UTC()
	planned := staleTrainV2ForRetirementTest(now)
	planned.Status = model.TrainV2Planned
	planned.Items[0].Status = model.TrainV2ItemQueued
	planned.Items[0].Attempts = nil
	if staticTrainV2SafeToRetire(planned) {
		t.Fatal("planned Train was classified as safe to retire")
	}
	integration := staleTrainV2ForRetirementTest(now)
	integration.Status = model.TrainV2ReadyForIntegration
	if staticTrainV2SafeToRetire(integration) {
		t.Fatal("integration-pending Train was classified as safe to retire")
	}
}
func TestTrainV2ReconcileDryRunApplyAndRetryAreBounded(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed reconcile train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := trainV2RetirementTestContext()
	dry, err := s.TrainV2Reconcile(ctx, TrainV2ReconcileInput{
		ProjectID: "example",
		Apply:     false,
		Reason:    "bounded test",
	})
	if err != nil || !dry.DryRun || len(dry.Records) != 1 || !dry.Records[0].SafeToRetire {
		t.Fatalf("unexpected dry-run: %#v %v", dry, err)
	}
	apply, err := s.TrainV2Reconcile(ctx, TrainV2ReconcileInput{
		ProjectID: "example",
		Apply:     true,
		Reason:    "bounded test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil || apply.Hub.Status != "reconciled" {
		t.Fatalf("unexpected apply: %#v %v", apply, err)
	}
	retry, err := s.TrainV2Reconcile(ctx, TrainV2ReconcileInput{
		ProjectID: "example",
		Apply:     true,
		Reason:    "bounded test",
	})
	if err != nil || retry.Hub.Status != "no_changes" {
		t.Fatalf("reconcile retry was not idempotent: %#v %v", retry, err)
	}
}
func TestTrainV2RetireAsyncDoesNotSeeItsOwnReceiptAsLiveExecution(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed async retirement train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := trainV2RetirementTestContext()
	receipt, err := s.TrainV2RetireAsync(ctx, TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "async bounded test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitDurableMutationTerminal(t, s, receipt.OperationID)
	status, err := s.TrainV2RetirementOperationStatus(ctx, receipt.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "completed" || status.Retirement == nil || status.Retirement.Status != model.TrainV2Retired {
		t.Fatalf("async retirement did not complete: %#v", status)
	}
}
func TestTrainV2RetirementReconcilesLiveOperationAfterServiceRestart(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(time.Now().UTC())
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed restart reconciliation train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seedLiveTrainMutationForRetirementTest(t, s, "train-attempt-review")
	restarted := &Service{
		Config:     s.Config,
		ConfigPath: s.ConfigPath,
		Hub:        s.Hub,
		clock:      s.clock,
	}
	_, err = restarted.TrainV2Retire(trainV2RetirementTestContext(), TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "restart must preserve live-operation guard",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "TRAIN_OPERATION_LIVE") {
		t.Fatalf("restart reconciliation lost live operation: %v", err)
	}
}
