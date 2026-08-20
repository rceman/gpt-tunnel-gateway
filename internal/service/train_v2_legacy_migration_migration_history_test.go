package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestTrainV2LegacyMigrationMarksHistoricalWithExactDigestAndIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := reviewBackfillFixture(t)
	train.ID = "EXM-TRN1"
	train.Status = model.TrainV2RecoveryQuarantined
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed historical Train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	path := s.trainV2Path("example", train.ID)
	original, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestBytes(original)
	ctx := trainV2RetirementTestContext()
	input := TrainV2LegacyStateMigrationInput{
		ProjectID: "example",
		Reason:    "preserve pre-cutover history",
		Actions:   []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionMarkHistorical, TrainID: train.ID, TrainSHA256: digest}},
	}
	dry, err := s.TrainV2MigrateLegacyState(ctx, input)
	if err != nil || !dry.DryRun || len(dry.Records) != 1 {
		t.Fatalf("unexpected dry-run: %#v err=%v", dry, err)
	}
	unchanged, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("dry-run changed historical bytes: err=%v", err)
	}
	applyInput := input
	applyInput.Apply = true
	applyInput.ExpectedHubRevision = dry.HubBefore
	applied, err := s.TrainV2MigrateLegacyState(ctx, applyInput)
	if err != nil || !applied.Applied {
		t.Fatalf("migration apply failed: %#v err=%v", applied, err)
	}
	var migrated model.TrainV2
	if err := s.Hub.ReadJSON(context.Background(), path, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Historical == nil || migrated.Historical.SourceSHA256 != digest || migrated.Status != model.TrainV2RecoveryQuarantined {
		t.Fatalf("historical disposition missing: %#v", migrated)
	}
	retry, err := s.TrainV2MigrateLegacyState(ctx, input)
	if err != nil || !retry.AlreadyDone {
		t.Fatalf("migration was not idempotent: %#v err=%v", retry, err)
	}
	bad := input
	bad.Actions = []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionMarkHistorical, TrainID: train.ID, TrainSHA256: strings.Repeat("f", 64)}}
	if _, err := s.TrainV2MigrateLegacyState(ctx, bad); err == nil {
		t.Fatal("stale digest was accepted")
	}
}
func TestTrainV2LegacyMigrationRecoversFailedIntegrationByDigest(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := reviewBackfillFixture(t)
	train.ID = "EXM-TRN2"
	train.Status = model.TrainV2ReadyForIntegration
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed integration migration", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var err error
	revision, err = s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	op := trainv2.IntegrationOperation{SchemaVersion: 1, OperationID: "GTW-INTEGRATE-000000000000000000000001", ProjectID: "example", TrainID: train.ID, RequestSHA256: strings.Repeat("a", 64), SourceHead: strings.Repeat("b", 40), TargetBranch: "main", TargetBefore: strings.Repeat("c", 40), Phase: trainv2.IntegrationPhasePrePending, UpdatedAt: now}
	seedTrainIntegrationOperation(t, s, revision, op)
	mutationInput := []byte(`{"project_id":"example","train_id":"EXM-TRN2"}`)
	if err := s.writeDurableMutation(durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   "mutation-legacy-integration",
		Kind:          "train-v2-integrate",
		RequestSHA256: durableMutationDigest("train-v2-integrate", "", mutationInput),
		ProjectID:     "example",
		Input:         mutationInput,
		Status:        "failed",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, "mutation-legacy-integration")) })
	trainRaw, err := s.Hub.ReadFile(context.Background(), s.trainV2Path("example", train.ID))
	if err != nil {
		t.Fatal(err)
	}
	opRaw, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationPath("example", train.ID))
	if err != nil {
		t.Fatal(err)
	}
	mutationRaw, err := os.ReadFile(durableMutationPath(s.Config.StateDir, "mutation-legacy-integration"))
	if err != nil {
		t.Fatal(err)
	}
	input := TrainV2LegacyStateMigrationInput{
		ProjectID:           "example",
		Apply:               true,
		ExpectedHubRevision: mustHubRevision(t, s),
		Reason:              "reconcile failed integration prefix",
		Actions:             []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionRecoverIntegrate, TrainID: train.ID, TrainSHA256: digestBytes(trainRaw), IntegrationSHA256: digestBytes(opRaw), IntegrationMutationID: "mutation-legacy-integration", IntegrationMutationSHA256: digestBytes(mutationRaw)}},
	}
	result, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), input)
	if err != nil || !result.Applied {
		t.Fatalf("integration migration failed: %#v err=%v", result, err)
	}
	var recovered trainv2.IntegrationOperation
	if err := s.Hub.ReadJSON(context.Background(), trainV2IntegrationOperationPath("example", train.ID), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != trainv2.IntegrationPhaseRecoveryRequired {
		t.Fatalf("stale operation was not moved to recovery: %#v", recovered)
	}
	history, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath("example", train.ID, op.OperationID))
	if err != nil || string(history) != string(opRaw) {
		t.Fatalf("original integration bytes were not preserved: err=%v", err)
	}
	retryInput := input
	retryInput.ExpectedHubRevision = mustHubRevision(t, s)
	retry, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), retryInput)
	if err != nil || !retry.AlreadyDone {
		t.Fatalf("integration migration was not idempotent: %#v err=%v", retry, err)
	}
	wrongMutationID := retryInput
	wrongMutationID.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	wrongMutationID.Actions[0].IntegrationMutationID = "mutation-other-integration"
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), wrongMutationID); err == nil {
		t.Fatal("receipt accepted a different integration mutation ID")
	}
	wrongMutationDigest := retryInput
	wrongMutationDigest.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	wrongMutationDigest.Actions[0].IntegrationMutationSHA256 = strings.Repeat("f", 64)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), wrongMutationDigest); err == nil {
		t.Fatal("receipt accepted a different integration mutation digest")
	}
	pathTraversal := retryInput
	pathTraversal.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	pathTraversal.Actions[0].IntegrationMutationID = "../outside-state"
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), pathTraversal); err == nil {
		t.Fatal("path-traversal integration mutation ID was accepted")
	}
	receiptRaw, err := s.Hub.ReadFile(context.Background(), result.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	evidencePath := trainV2LegacyMigrationEvidencePath("example", train.ID, digestBytes(trainRaw))
	evidenceRaw, err := s.Hub.ReadFile(context.Background(), evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt model.TrainV2LegacyStateMigrationReceipt
	if err := decodeStrict(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	var evidence model.TrainV2LegacyStateMigrationRecord
	if err := decodeStrict(evidenceRaw, &evidence); err != nil {
		t.Fatal(err)
	}
	writeHubJSON := func(path string, value any) {
		t.Helper()
		revision := mustHubRevision(t, s)
		if _, err := s.Hub.Transact(context.Background(), revision, "test: tamper migration evidence", func(worktree string) ([]string, error) {
			if err := hub.WriteJSON(worktree, path, value); err != nil {
				return nil, err
			}
			return []string{path}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	tamperedReceipt := receipt
	tamperedReceipt.Reason = "tampered receipt"
	writeHubJSON(result.ReceiptPath, tamperedReceipt)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), retryInput); err == nil {
		t.Fatal("tampered migration receipt was accepted")
	}
	writeHubJSON(result.ReceiptPath, receipt)
	tamperedEvidence := evidence
	tamperedEvidence.TrainPath = s.trainV2Path("example", "EXM-TRN3")
	writeHubJSON(evidencePath, tamperedEvidence)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), retryInput); err == nil {
		t.Fatal("tampered migration evidence was accepted")
	}
	writeHubJSON(evidencePath, evidence)
	var malformedMutation durableMutationOperation
	if err := decodeStrict(mutationRaw, &malformedMutation); err != nil {
		t.Fatal(err)
	}
	malformedMutation.Input = json.RawMessage(`{}`)
	malformedMutationRaw, err := json.Marshal(malformedMutation)
	if err != nil {
		t.Fatal(err)
	}
	malformedRecord := evidence
	malformedRecord.MutationSHA256 = digestBytes(malformedMutationRaw)
	malformedRecord.OriginalMutationJSONB64 = base64.StdEncoding.EncodeToString(malformedMutationRaw)
	malformedReceipt := receipt
	malformedReceipt.Records = append([]model.TrainV2LegacyStateMigrationRecord{}, receipt.Records...)
	malformedReceipt.Records[0] = malformedRecord
	writeHubJSON(evidencePath, malformedRecord)
	writeHubJSON(result.ReceiptPath, malformedReceipt)
	if err := os.WriteFile(durableMutationPath(s.Config.StateDir, "mutation-legacy-integration"), malformedMutationRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	malformedInput := retryInput
	malformedInput.ExpectedHubRevision = mustHubRevision(t, s)
	malformedInput.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	malformedInput.Actions[0].IntegrationMutationSHA256 = digestBytes(malformedMutationRaw)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), malformedInput); err == nil {
		t.Fatal("malformed original mutation evidence was accepted")
	}
}
