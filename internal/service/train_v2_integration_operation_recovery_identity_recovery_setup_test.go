package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

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
