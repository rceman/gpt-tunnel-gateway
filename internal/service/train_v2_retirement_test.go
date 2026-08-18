package service

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func trainV2RetirementTestContext() context.Context {
	return WithAgentSessionID(authority.WithPlanner(context.Background()), "SP-ABCDEFGH")
}

func staleTrainV2ForRetirementTest(now time.Time) model.TrainV2 {
	finished := now.Add(-time.Minute)
	return model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion, ID: "EXM-TRN1", ProjectID: "example", Revision: 2,
		Status: model.TrainV2Blocked, CreatedBy: "planner", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Items: []model.TrainV2Item{{Position: 0, TaskID: "EXM-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemBlocked, AddedAt: now.Add(-time.Hour), Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptFailed, AgentID: "agent-1", AirelaySessionKey: "session-1", GatewayID: "gateway-1", StartHead: strings.Repeat("b", 40), StartedAt: now.Add(-2 * time.Hour), FinishedAt: &finished}}}},
	}
}

func seedLiveTrainMutationForRetirementTest(t *testing.T, s *Service, kind string) string {
	t.Helper()
	operationID := "mutation-test-" + strings.ReplaceAll(kind, "_", "-")
	now := time.Now().UTC()
	if err := s.writeDurableMutation(durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          kind,
		RequestSHA256: durableMutationDigest(kind, "", []byte(`{"train_id":"EXM-TRN1"}`)),
		ProjectID:     "example",
		Input:         []byte(`{"train_id":"EXM-TRN1"}`),
		Status:        "running",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, operationID)) })
	return operationID
}

func TestTrainV2RetireRecordsServerOwnedEvidenceAndIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	train := staleTrainV2ForRetirementTest(now)
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed stale train", func(worktree string) ([]string, error) {
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
	input := TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "terminal failed Attempt has no live owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	}
	retired, err := s.TrainV2Retire(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if retired.Status != model.TrainV2Retired || retired.Train.Retirement == nil || retired.Train.Retirement.ActorSessionID != "SP-ABCDEFGH" || retired.Train.Retirement.PreviousStatus != model.TrainV2Blocked {
		t.Fatalf("retirement evidence missing: %#v", retired)
	}
	second, err := s.TrainV2Retire(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.Train.Revision != retired.Train.Revision || second.Status != model.TrainV2Retired {
		t.Fatalf("retirement was not idempotent: %#v", second)
	}
}

func TestTrainV2RetireRejectsLiveAttempt(t *testing.T) {
	now := time.Now().UTC()
	train := staleTrainV2ForRetirementTest(now)
	train.Status = model.TrainV2Running
	train.Items[0].Status = model.TrainV2ItemRunning
	train.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	train.Items[0].Attempts[0].FinishedAt = nil
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed live train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TrainV2Retire(trainV2RetirementTestContext(), TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "must fail",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "TRAIN_ATTEMPT_LIVE") {
		t.Fatalf("live Train was not rejected: %v", err)
	}
}

func TestTrainV2LiveOperationRecognizesAttemptMutationKinds(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	for _, kind := range []string{"train-attempt-finalize", "train-attempt-review", "train-attempt-proof-recovery"} {
		t.Run(kind, func(t *testing.T) {
			seedLiveTrainMutationForRetirementTest(t, s, kind)
			live, err := s.trainV2HasLiveOperation("example", "EXM-TRN1")
			if err != nil || !live {
				t.Fatalf("kind %q live=%v err=%v", kind, live, err)
			}
		})
	}
}

func TestTrainV2LiveOperationUnknownKindFailsClosed(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	seedLiveTrainMutationForRetirementTest(t, s, "train-v2-future-mutation")
	if _, err := s.trainV2HasLiveOperation("example", "EXM-TRN1"); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("unknown Train operation did not fail closed: %v", err)
	}
}

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

func seedTrainV2RecordsForRetirementTest(t *testing.T, s *Service, revision string, trains ...model.TrainV2) string {
	t.Helper()
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed Train conflict records", func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(trains))
		for _, train := range trains {
			path := s.trainV2Path("example", train.ID)
			if err := hub.WriteJSON(worktree, path, train); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		return paths, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx.After
}

func TestTrainV2StartDoesNotRejectDisjointActiveTrain(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	active := staleTrainV2ForRetirementTest(now)
	active.ID = "EXM-TRN2"
	active.Status = model.TrainV2Running
	active.Items[0].Status = model.TrainV2ItemRunning
	active.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	active.Items[0].Attempts[0].FinishedAt = nil
	target := staleTrainV2ForRetirementTest(now)
	target.ID = "EXM-TRN3"
	target.Status = model.TrainV2Planned
	target.Items[0].Status = model.TrainV2ItemQueued
	target.Items[0].Attempts = nil
	revision = seedTrainV2RecordsForRetirementTest(t, s, revision, active, target)
	_, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   target.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil && strings.Contains(err.Error(), "TRAIN_ACTIVE_CONFLICT") {
		t.Fatalf("Train start retained project-wide active Train rejection: %v", err)
	}
}

func TestTrainV2AdvanceDoesNotRejectDisjointActiveTrain(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	active := staleTrainV2ForRetirementTest(now)
	active.ID = "EXM-TRN2"
	active.Status = model.TrainV2Running
	active.Items[0].Status = model.TrainV2ItemRunning
	active.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	active.Items[0].Attempts[0].FinishedAt = nil
	target := staleTrainV2ForRetirementTest(now)
	target.ID = "EXM-TRN3"
	target.Status = model.TrainV2Running
	revision = seedTrainV2RecordsForRetirementTest(t, s, revision, active, target)
	_, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   target.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil && strings.Contains(err.Error(), "TRAIN_ACTIVE_CONFLICT") {
		t.Fatalf("Train advance retained project-wide active Train rejection: %v", err)
	}
}

func TestTrainV2WorkerRecoversRunningReconcileAfterRestart(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	_ = enableTrainV2ForTest(t, s, revision)
	operationID := "mutation-restart-reconcile"
	input := []byte(`{"apply":false,"project_id":"example","reason":"restart recovery"}`)
	now := time.Now().UTC()
	if err := s.writeDurableMutation(durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "train-v2-reconcile",
		RequestSHA256: durableMutationDigest("train-v2-reconcile", "", input),
		ProjectID:     "example",
		Input:         input,
		Status:        "running",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Config)
	waitDurableMutationTerminal(t, restarted, operationID)
	operation, err := restarted.readDurableMutation(operationID)
	if err != nil || operation.Status != "completed" || operation.RecoveryReason == "" {
		t.Fatalf("running mutation was not recovered after restart: %#v err=%v", operation, err)
	}
}
