package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) verifyLegacyMigrationReceipt(ctx context.Context, receipt model.TrainV2LegacyStateMigrationReceipt, in TrainV2LegacyStateMigrationInput) error {
	if len(in.Actions) != len(receipt.Records) {
		return fmt.Errorf("legacy Train migration receipt action mismatch")
	}
	byTrain := make(map[string]model.TrainV2LegacyStateMigrationRecord, len(receipt.Records))
	for _, record := range receipt.Records {
		byTrain[record.TrainID] = record
	}
	for _, action := range in.Actions {
		record, ok := byTrain[action.TrainID]
		if !ok || record.Action != action.Action || record.TrainSHA256 != action.TrainSHA256 || record.IntegrationSHA256 != action.IntegrationSHA256 || record.MutationSHA256 != action.IntegrationMutationSHA256 {
			return fmt.Errorf("legacy Train migration receipt does not match requested digest set")
		}
		expectedTrainPath := s.trainV2Path(in.ProjectID, record.TrainID)
		if record.TrainPath != expectedTrainPath {
			return fmt.Errorf("legacy Train migration Train path identity changed for %s", record.TrainID)
		}
		if action.Action == TrainV2LegacyActionRecoverIntegrate {
			expectedMutationPath := filepath.ToSlash(filepath.Join("operations", "mutations", action.IntegrationMutationID+".json"))
			if action.IntegrationMutationID == "" || record.MutationPath != expectedMutationPath {
				return fmt.Errorf("legacy Train migration mutation path identity changed for %s", record.TrainID)
			}
		} else if action.IntegrationMutationID != "" || action.IntegrationMutationSHA256 != "" || record.MutationPath != "" || record.MutationSHA256 != "" {
			return fmt.Errorf("unexpected integration mutation identity for %s", record.TrainID)
		}
		evidenceRaw, err := s.Hub.ReadFile(ctx, trainV2LegacyMigrationEvidencePath(in.ProjectID, record.TrainID, record.TrainSHA256))
		if err != nil {
			return err
		}
		var evidence model.TrainV2LegacyStateMigrationRecord
		if err := decodeStrict(evidenceRaw, &evidence); err != nil || evidence != record {
			return fmt.Errorf("legacy Train migration evidence mismatch for %s", record.TrainID)
		}
		raw, err := s.Hub.ReadFile(ctx, record.TrainPath)
		if err != nil {
			return err
		}
		var train model.TrainV2
		if err := decodeStrict(raw, &train); err != nil {
			return err
		}
		if train.ID != record.TrainID || train.ProjectID != in.ProjectID {
			return fmt.Errorf("legacy Train migration Train identity changed for %s", record.TrainID)
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return err
		}
		switch record.Action {
		case TrainV2LegacyActionMarkHistorical:
			if record.TrainPath != s.trainV2Path(in.ProjectID, record.TrainID) || train.Historical == nil || train.Historical.SourcePath != record.TrainPath || train.Historical.SourceSHA256 != record.TrainSHA256 {
				return fmt.Errorf("historical Train migration state is incomplete for %s", train.ID)
			}
		case TrainV2LegacyActionRecoverIntegrate:
			opRaw, err := s.Hub.ReadFile(ctx, record.IntegrationPath)
			if err != nil {
				return err
			}
			if record.IntegrationPath != trainV2IntegrationOperationPath(in.ProjectID, record.TrainID) {
				return fmt.Errorf("integration migration source identity changed for %s", train.ID)
			}
			originalOpRaw, err := decodeBytes(record.OriginalIntegrationJSONB64)
			if err != nil {
				return err
			}
			var originalOp trainv2.IntegrationOperation
			if err := decodeStrict(originalOpRaw, &originalOp); err != nil {
				return err
			}
			var op trainv2.IntegrationOperation
			if err := decodeStrict(opRaw, &op); err != nil || op.Phase != trainv2.IntegrationPhaseRecoveryRequired {
				return fmt.Errorf("integration migration state is incomplete for %s", train.ID)
			}
			if op.OperationID != originalOp.OperationID || op.ProjectID != originalOp.ProjectID || op.TrainID != originalOp.TrainID || op.RequestSHA256 != originalOp.RequestSHA256 || op.SourceHead != originalOp.SourceHead || op.TargetBranch != originalOp.TargetBranch || op.TargetBefore != originalOp.TargetBefore {
				return fmt.Errorf("integration migration operation identity changed for %s", train.ID)
			}
			mutationRaw, err := os.ReadFile(filepath.Join(s.Config.StateDir, filepath.FromSlash(record.MutationPath)))
			if err != nil || digestBytes(mutationRaw) != record.MutationSHA256 {
				return fmt.Errorf("integration migration mutation identity changed for %s", train.ID)
			}
			var mutation durableMutationOperation
			if err := decodeStrict(mutationRaw, &mutation); err != nil || mutation.OperationID != filepath.Base(strings.TrimSuffix(record.MutationPath, ".json")) || mutation.Status != "failed" || mutation.Kind != "train-v2-integrate" || mutation.ProjectID != in.ProjectID {
				return fmt.Errorf("integration migration mutation state is incomplete for %s", train.ID)
			}
			originalMutationRaw, err := decodeBytes(record.OriginalMutationJSONB64)
			if err != nil {
				return err
			}
			var originalMutation durableMutationOperation
			if err := decodeStrict(originalMutationRaw, &originalMutation); err != nil {
				return fmt.Errorf("invalid original integration mutation evidence for %s: %w", train.ID, err)
			}
			if originalMutation.SchemaVersion != durableMutationSchemaVersion || originalMutation.OperationID != action.IntegrationMutationID || originalMutation.Kind != "train-v2-integrate" || originalMutation.ProjectID != in.ProjectID || originalMutation.Status != "failed" || string(originalMutation.Input) != string(mutation.Input) || originalMutation.RequestSHA256 != mutation.RequestSHA256 {
				return fmt.Errorf("original integration mutation evidence identity changed for %s", train.ID)
			}
			var mutationInput TrainV2IntegrateInput
			if err := decodeStrict(originalMutation.Input, &mutationInput); err != nil || mutationInput.ProjectID != in.ProjectID || mutationInput.TrainID != record.TrainID {
				return fmt.Errorf("original integration mutation evidence target changed for %s", train.ID)
			}
		}
	}
	return nil
}
