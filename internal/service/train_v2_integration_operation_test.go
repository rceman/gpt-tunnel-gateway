package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func seedTrainIntegrationOperation(t *testing.T, s *Service, expected string, operation trainv2.IntegrationOperation) {
	t.Helper()
	if _, err := s.Hub.Transact(context.Background(), expected, "test: seed Train integration operation", func(worktree string) ([]string, error) {
		path := trainV2IntegrationOperationPath(operation.ProjectID, operation.TrainID)
		if err := hub.WriteJSON(worktree, path, operation); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func seedMigratedRecoveryEvidence(t *testing.T, s *Service, revision string, trainID, source, target string) (trainv2.IntegrationOperation, []byte) {
	t.Helper()
	now := nowUTC()
	train, trainErr := func() (model.TrainV2, error) {
		raw, err := s.Hub.ReadFile(context.Background(), s.trainV2Path("example", trainID))
		if err == nil {
			var existing model.TrainV2
			if decodeErr := decodeStrict(raw, &existing); decodeErr != nil {
				return model.TrainV2{}, decodeErr
			}
			return existing, nil
		}
		fixture, _ := reviewBackfillFixture(t)
		return fixture, nil
	}()
	if trainErr != nil {
		t.Fatal(trainErr)
	}
	train.ID = trainID
	train.Status = model.TrainV2ReadyForIntegration
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   trainID,
	}
	digest, operationID, err := integrationRequestDigest(input, source, "main", target)
	if err != nil {
		t.Fatal(err)
	}
	archived := trainv2.IntegrationOperation{SchemaVersion: 1, OperationID: operationID, ProjectID: "example", TrainID: trainID, RequestSHA256: digest, SourceHead: source, TargetBranch: "main", TargetBefore: target, Phase: trainv2.IntegrationPhasePrePending, UpdatedAt: now}
	archivedRaw, err := json.Marshal(archived)
	if err != nil {
		t.Fatal(err)
	}
	recovery := archived
	recovery.Phase = trainv2.IntegrationPhaseRecoveryRequired
	recovery.RecoveryReason = "fresh read-only legacy Train migration plan"
	mutationID := "mutation-legacy-recovery-303"
	mutationInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	mutation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   mutationID,
		Kind:          "train-v2-integrate",
		RequestSHA256: durableMutationDigest("train-v2-integrate", "", mutationInput),
		ProjectID:     "example",
		Input:         mutationInput,
		Status:        "failed",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(mutation); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, mutationID)) })
	if tx, err := s.Hub.Transact(context.Background(), revision, "test: seed migrated recovery operation", func(worktree string) ([]string, error) {
		trainPath := s.trainV2Path("example", trainID)
		opPath := trainV2IntegrationOperationPath("example", trainID)
		historyPath := trainV2IntegrationOperationHistoryPath("example", trainID, operationID)
		if err := hub.WriteJSON(worktree, trainPath, train); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, opPath, recovery); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, historyPath, string(archivedRaw)); err != nil {
			return nil, err
		}
		return []string{trainPath, opPath, historyPath}, nil
	}); err != nil {
		t.Fatal(err)
	} else {
		revision = tx.After
	}
	trainRaw, err := s.Hub.ReadFile(context.Background(), s.trainV2Path("example", trainID))
	if err != nil {
		t.Fatal(err)
	}
	mutationRaw, err := os.ReadFile(durableMutationPath(s.Config.StateDir, mutationID))
	if err != nil {
		t.Fatal(err)
	}
	record := model.TrainV2LegacyStateMigrationRecord{
		Action:                     TrainV2LegacyActionRecoverIntegrate,
		TrainID:                    trainID,
		TrainPath:                  s.trainV2Path("example", trainID),
		TrainSHA256:                digestBytes(trainRaw),
		OriginalTrainJSONB64:       encodeBytes(trainRaw),
		IntegrationPath:            trainV2IntegrationOperationPath("example", trainID),
		IntegrationSHA256:          digestBytes(archivedRaw),
		OriginalIntegrationJSONB64: encodeBytes(archivedRaw),
		MutationPath:               "operations/mutations/" + mutationID + ".json",
		MutationSHA256:             digestBytes(mutationRaw),
		OriginalMutationJSONB64:    encodeBytes(mutationRaw),
	}
	receipt := model.TrainV2LegacyStateMigrationReceipt{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: "example", State: "completed", HubBefore: strings.Repeat("a", 40), HubAfter: strings.Repeat("b", 40), Records: []model.TrainV2LegacyStateMigrationRecord{record}, Reason: "fresh read-only legacy Train migration plan", CreatedAt: now, UpdatedAt: now}
	evidencePath := trainV2LegacyMigrationEvidencePath("example", trainID, record.TrainSHA256)
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed migration receipt", func(worktree string) ([]string, error) {
		receiptPath := trainV2LegacyMigrationReceiptPath("example")
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, evidencePath, record); err != nil {
			return nil, err
		}
		return []string{receiptPath, evidencePath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return recovery, archivedRaw
}

func TestIntegrationOperationResumesMigratedRecoveryWithFreshIdentityAndPreservesEvidence(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN303",
	}
	source := strings.Repeat("c", 40)
	target := strings.Repeat("d", 40)
	old, archivedRaw := seedMigratedRecoveryEvidence(t, s, revision, input.TrainID, source, target)
	resumed, err := s.integrationOperation(context.Background(), input, source, "main", target, false, nowUTC())
	if err != nil {
		t.Fatalf("migrated recovery did not resume: %v", err)
	}
	if resumed.OperationID == old.OperationID || resumed.SupersedesOperationID != old.OperationID || resumed.Phase != trainv2.IntegrationPhasePrePending || resumed.SourceHead != source || resumed.TargetBefore != target {
		t.Fatalf("fresh recovery operation=%#v", resumed)
	}
	history, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(input.ProjectID, input.TrainID, old.OperationID))
	if err != nil || !bytes.Equal(history, archivedRaw) {
		t.Fatalf("archived operation changed: err=%v", err)
	}
	restarted := &Service{
		Config: s.Config,
		Hub:    s.Hub,
	}
	retry, err := restarted.integrationOperation(context.Background(), input, source, "main", target, false, nowUTC())
	if err != nil || retry.OperationID != resumed.OperationID {
		t.Fatalf("recovery retry after restart was not idempotent: operation=%#v err=%v", retry, err)
	}
}

func TestMigratedRecoveryIdempotentRetrySelfChecksFreshIdentity(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN306",
	}
	source := strings.Repeat("c", 40)
	target := strings.Repeat("d", 40)
	seedMigratedRecoveryEvidence(t, s, revision, input.TrainID, source, target)
	resumed, err := s.integrationOperation(context.Background(), input, source, "main", target, false, nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	resumed.OperationID = "GTW-INTEGRATE-" + strings.Repeat("f", 24)
	resumed.RequestSHA256 = strings.Repeat("f", 64)
	if err := s.persistIntegrationOperation(context.Background(), resumed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.integrationOperation(context.Background(), input, source, "main", target, false, nowUTC()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered fresh recovery identity was accepted: %v", err)
	}
}

func prepareCanonicalRecoveryIntegration(t *testing.T, trainID string) (*Service, TrainV2IntegrateInput, string) {
	t.Helper()
	s, revision, projectHead := testServiceWithoutIdentifiers(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := reviewBackfillFixture(t)
	train.ID = trainID
	train.Items[0].Status = model.TrainV2ItemReviewed
	train.Items[0].Attempts[0].StartHead = projectHead
	train.Items[0].Proof.CheckpointHead = projectHead
	train.Items[0].Proof.ImplementationSHA = projectHead
	train.Items[0].Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: train.Items[0].Proof.ReportID, ReviewedAt: nowUTC()}
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	branch := "train/" + trainID
	if err := s.Git.CreateTrainWorktree(context.Background(), project, s.Config.StateDir, "example", trainID, branch, projectHead); err != nil {
		t.Fatal(err)
	}
	worktree := trainv2.ExpectedWorktreePath(s.Config.StateDir, "example", train.ID)
	now := nowUTC()
	start := model.TrainV2StartRecord{
		SchemaVersion:             model.TrainV2StartSchemaVersion,
		ProjectID:                 "example",
		TrainID:                   train.ID,
		Status:                    model.TrainV2StartActive,
		IntegrationBranch:         "main",
		BaseRevision:              projectHead,
		LaneBranch:                branch,
		CurrentItemPosition:       0,
		CurrentAttemptNumber:      1,
		CurrentTaskID:             train.Items[0].TaskID,
		CurrentTaskRevision:       train.Items[0].TaskRevision,
		CurrentTaskRevisionSHA256: train.Items[0].TaskRevisionSHA256,
		StartedAt:                 now,
	}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "example", TrainID: train.ID, WorktreePath: worktree, AgentID: "agent", SessionKey: "session", ItemPosition: 0, TaskID: train.Items[0].TaskID, AttemptNumber: 1, StartedAt: now}
	proved, err := trainv2.RecordFullProof(train, projectHead, []model.CompletionGateResult{
		{ID: model.WorkflowGateFormat, ExitCode: 0},
		{ID: model.WorkflowGateCheck, ExitCode: 0},
		{ID: model.WorkflowGateTest, ExitCode: 0},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	startPath := hub.ProtocolRoot + "/projects/example/train-v2-starts/" + train.ID + ".json"
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed canonical recovery integration", func(worktree string) ([]string, error) {
		trainPath := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, trainPath, proved); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, startPath, start); err != nil {
			return nil, err
		}
		return []string{trainPath, startPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, "example", train.ID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   train.ID,
	}
	seedMigratedRecoveryEvidence(t, s, tx.After, train.ID, projectHead, projectHead)
	return s, input, projectHead
}

func TestMigratedRecoveryCompletesThroughTrainV2IntegrateAndReceipt(t *testing.T) {
	s, input, head := prepareCanonicalRecoveryIntegration(t, "GTW-TRN307")
	receipt, _, err := s.TrainV2Integrate(context.Background(), input)
	if err != nil || receipt.Status != "completed" {
		t.Fatalf("canonical recovery integration failed: receipt=%#v err=%v", receipt, err)
	}
	stored, err := s.readTrainV2IntegrationReceipt(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || stored.Status != "completed" || stored.IntegrationHead != head {
		t.Fatalf("completion receipt=%#v err=%v", stored, err)
	}
	train, err := s.TrainV2Read(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || train.Status != model.TrainV2Completed {
		t.Fatalf("Train was not marked integrated: %#v err=%v", train, err)
	}
}

func TestMigratedRecoveryCompetingOwnerIsBlockedByTrainV2Integrate(t *testing.T) {
	s, input, _ := prepareCanonicalRecoveryIntegration(t, "GTW-TRN308")
	owner, err := s.acquireTrainV2IntegrationLock(context.Background(), input.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = owner.Release()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := s.TrainV2Integrate(ctx, input)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("TrainV2Integrate bypassed competing owner: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
	released = true
	if err := <-result; err != nil {
		t.Fatalf("TrainV2Integrate failed after competing owner released: %v", err)
	}
}

func TestIntegrationOperationRejectsMigratedRecoverySourceOrTargetDrift(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN304",
	}
	source := strings.Repeat("c", 40)
	target := strings.Repeat("d", 40)
	old, _ := seedMigratedRecoveryEvidence(t, s, revision, input.TrainID, source, target)
	if _, err := s.integrationOperation(context.Background(), input, strings.Repeat("e", 40), "main", target, false, nowUTC()); err == nil || !strings.Contains(err.Error(), "migrated identity") {
		t.Fatalf("source drift was accepted: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || current.OperationID != old.OperationID || current.Phase != trainv2.IntegrationPhaseRecoveryRequired {
		t.Fatalf("source drift mutated recovery operation: %#v err=%v", current, err)
	}
	if _, err := s.integrationOperation(context.Background(), input, source, "main", strings.Repeat("e", 40), false, nowUTC()); err == nil || !strings.Contains(err.Error(), "migrated identity") {
		t.Fatalf("target drift was accepted: %v", err)
	}
	current, err = s.readIntegrationOperation(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || current.OperationID != old.OperationID || current.Phase != trainv2.IntegrationPhaseRecoveryRequired {
		t.Fatalf("target drift mutated recovery operation: %#v err=%v", current, err)
	}
}

func TestMigratedRecoveryOperationReachesCompletedPhase(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN305",
	}
	source := strings.Repeat("c", 40)
	target := strings.Repeat("d", 40)
	seedMigratedRecoveryEvidence(t, s, revision, input.TrainID, source, target)
	operation, err := s.integrationOperation(context.Background(), input, source, "main", target, false, nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{
		trainv2.IntegrationPhasePreComplete,
		trainv2.IntegrationPhaseIntegratePending,
		trainv2.IntegrationPhaseIntegrateComplete,
		trainv2.IntegrationPhasePostPending,
		trainv2.IntegrationPhaseCompleted,
	} {
		operation, err = s.advanceIntegrationOperation(context.Background(), operation, phase, phase+" evidence")
		if err != nil {
			t.Fatalf("advance to %s: %v", phase, err)
		}
	}
	current, err := s.readIntegrationOperation(context.Background(), input.ProjectID, input.TrainID)
	if err != nil || current.OperationID != operation.OperationID || current.Phase != trainv2.IntegrationPhaseCompleted {
		t.Fatalf("completed recovery operation=%#v err=%v", current, err)
	}
}

func TestMigratedRecoveryCompetingIntegrationOwnerWaitsForProjectLock(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	first, err := s.acquireTrainV2IntegrationLock(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = first.Release()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		second, err := s.acquireTrainV2IntegrationLock(ctx, "example")
		if err == nil {
			_ = second.Release()
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("competing integration owner was admitted early: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	released = true
	if err := <-result; err != nil {
		t.Fatalf("queued recovery owner did not acquire after release: %v", err)
	}
}

func TestIntegrationOperationReplacesUntouchedStalePrePendingWithCurrentIdentity(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
	}
	oldSource := strings.Repeat("a", 40)
	oldTarget := strings.Repeat("b", 40)
	currentSource := strings.Repeat("c", 40)
	currentTarget := strings.Repeat("d", 40)
	oldDigest, oldOperationID, err := integrationRequestDigest(in, oldSource, "main", oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	old := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   oldOperationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		RequestSHA256: oldDigest,
		SourceHead:    oldSource,
		TargetBranch:  "main",
		TargetBefore:  oldTarget,
		Phase:         trainv2.IntegrationPhasePrePending,
		UpdatedAt:     time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, old)

	if _, err := s.integrationOperation(context.Background(), in, currentSource, "main", currentTarget, false, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("stale operation did not fail closed: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), in.ProjectID, in.TrainID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OperationID == old.OperationID || current.SourceHead != currentSource || current.TargetBefore != currentTarget || current.Phase != trainv2.IntegrationPhasePrePending {
		t.Fatalf("current recovery operation=%#v", current)
	}
	if current.SupersedesOperationID != old.OperationID {
		t.Fatalf("current operation did not identify superseded operation: %#v", current)
	}
	history, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(in.ProjectID, in.TrainID, old.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	var archived trainv2.IntegrationOperation
	if err := json.Unmarshal(history, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.OperationID != old.OperationID || archived.SourceHead != oldSource || archived.RequestSHA256 != oldDigest {
		t.Fatalf("archived stale operation=%#v", archived)
	}
	resumed, err := s.integrationOperation(context.Background(), in, currentSource, "main", currentTarget, false, time.Now().UTC())
	if err != nil {
		t.Fatalf("current operation did not resume after guarded recovery: %v", err)
	}
	if resumed.OperationID != current.OperationID || resumed.SupersedesOperationID != old.OperationID {
		t.Fatalf("resumed operation=%#v", resumed)
	}
}

func TestIntegrationOperationDoesNotRotateStalePrePendingWithPersistedHookEvidence(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN998",
	}
	oldSource := strings.Repeat("a", 40)
	oldTarget := strings.Repeat("b", 40)
	digest, operationID, err := integrationRequestDigest(in, oldSource, "main", oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	old := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   operationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		RequestSHA256: digest,
		SourceHead:    oldSource,
		TargetBranch:  "main",
		TargetBefore:  oldTarget,
		Phase:         trainv2.IntegrationPhasePrePending,
		PreResult:     "pre-hook evidence",
		UpdatedAt:     time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, old)

	if _, err := s.integrationOperation(context.Background(), in, strings.Repeat("c", 40), "main", strings.Repeat("d", 40), false, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "durable identity does not match") {
		t.Fatalf("operation with persisted hook evidence was not rejected: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), in.ProjectID, in.TrainID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OperationID != old.OperationID || current.SourceHead != oldSource || current.PreResult != old.PreResult {
		t.Fatalf("unsafe stale operation mutation=%#v", current)
	}
	if _, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(in.ProjectID, in.TrainID, old.OperationID)); err == nil {
		t.Fatal("unsafe stale operation created recovery history")
	}
}

func TestIntegrationOperationResumesPostPendingAfterTargetAdvanced(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN997",
	}
	source := strings.Repeat("c", 40)
	oldTarget := strings.Repeat("b", 40)
	digest, operationID, err := integrationRequestDigest(in, source, "main", oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	operation := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   operationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		RequestSHA256: digest,
		SourceHead:    source,
		TargetBranch:  "main",
		TargetBefore:  oldTarget,
		Phase:         trainv2.IntegrationPhasePostPending,
		PreResult:     "pre-hook evidence",
		UpdatedAt:     time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, operation)

	resumed, err := s.integrationOperation(context.Background(), in, source, "main", source, true, time.Now().UTC())
	if err != nil {
		t.Fatalf("post-pending operation did not resume after target advance: %v", err)
	}
	if resumed.OperationID != operationID || resumed.RequestSHA256 != digest || resumed.TargetBefore != oldTarget || resumed.PreResult != operation.PreResult || resumed.Phase != trainv2.IntegrationPhasePostPending {
		t.Fatalf("post-pending operation was rewritten: %#v", resumed)
	}
}

func TestIntegrationOperationSupersedesPostPendingPrefixAfterProvenSourceAdvance(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN996",
	}
	oldSource := strings.Repeat("a", 40)
	target := oldSource
	currentSource := strings.Repeat("c", 40)
	digest, operationID, err := integrationRequestDigest(in, oldSource, "main", strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	old := trainv2.IntegrationOperation{
		SchemaVersion: 1, OperationID: operationID, ProjectID: in.ProjectID, TrainID: in.TrainID,
		RequestSHA256: digest, SourceHead: oldSource, TargetBranch: "main", TargetBefore: strings.Repeat("b", 40),
		Phase: trainv2.IntegrationPhasePostPending, PreResult: "old pre evidence", UpdatedAt: time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, old)

	if _, err := s.integrationOperation(context.Background(), in, currentSource, "main", target, true, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "prefix archived") {
		t.Fatalf("post-pending prefix was not guarded for retry: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), in.ProjectID, in.TrainID)
	if err != nil {
		t.Fatal(err)
	}
	if current.SourceHead != currentSource || current.TargetBefore != target || current.Phase != trainv2.IntegrationPhasePrePending || current.SupersedesOperationID != old.OperationID || current.PreResult != "" {
		t.Fatalf("replacement operation=%#v", current)
	}
	history, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(in.ProjectID, in.TrainID, old.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	var archived trainv2.IntegrationOperation
	if err := json.Unmarshal(history, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.OperationID != old.OperationID || archived.SourceHead != oldSource || archived.TargetBefore != strings.Repeat("b", 40) || archived.PreResult != old.PreResult {
		t.Fatalf("archived post-pending operation changed: %#v", archived)
	}
}

func TestIntegrationOperationRejectsPostPendingPrefixWithoutAncestorProof(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN995",
	}
	oldSource := strings.Repeat("a", 40)
	digest, operationID, err := integrationRequestDigest(in, oldSource, "main", strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	seedTrainIntegrationOperation(t, s, revision, trainv2.IntegrationOperation{
		SchemaVersion: 1, OperationID: operationID, ProjectID: in.ProjectID, TrainID: in.TrainID,
		RequestSHA256: digest, SourceHead: oldSource, TargetBranch: "main", TargetBefore: strings.Repeat("b", 40),
		Phase: trainv2.IntegrationPhasePostPending, UpdatedAt: time.Now().UTC(),
	})
	if _, err := s.integrationOperation(context.Background(), in, strings.Repeat("c", 40), "main", oldSource, false, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "durable identity does not match") {
		t.Fatalf("unproven post-pending drift was accepted: %v", err)
	}
	if _, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(in.ProjectID, in.TrainID, operationID)); err == nil {
		t.Fatal("unproven post-pending drift created recovery history")
	}
}
