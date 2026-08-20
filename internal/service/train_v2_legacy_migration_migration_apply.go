package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) planTrainV2LegacyMigration(ctx context.Context, in TrainV2LegacyStateMigrationInput, reason string) ([]trainV2LegacyMigrationPlan, error) {
	actions := append([]TrainV2LegacyStateMigrationAction{}, in.Actions...)
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].TrainID < actions[j].TrainID || (actions[i].TrainID == actions[j].TrainID && actions[i].Action < actions[j].Action)
	})
	seen := map[string]struct{}{}
	plans := make([]trainV2LegacyMigrationPlan, 0, len(actions))
	for _, action := range actions {
		if action.Action != TrainV2LegacyActionMarkHistorical && action.Action != TrainV2LegacyActionRetireStale && action.Action != TrainV2LegacyActionRecoverIntegrate {
			return nil, fmt.Errorf("unknown legacy Train migration action %q", action.Action)
		}
		if _, _, err := model.ParseTrainV2ID(action.TrainID); err != nil || !validSHA256(action.TrainSHA256) {
			return nil, fmt.Errorf("invalid legacy Train migration identity for %s", action.TrainID)
		}
		if _, exists := seen[action.TrainID]; exists {
			return nil, fmt.Errorf("duplicate legacy Train migration target %s", action.TrainID)
		}
		seen[action.TrainID] = struct{}{}
		trainPath := s.trainV2Path(in.ProjectID, action.TrainID)
		raw, err := s.Hub.ReadFile(ctx, trainPath)
		if err != nil {
			return nil, err
		}
		if digestBytes(raw) != action.TrainSHA256 {
			return nil, fmt.Errorf("legacy Train digest mismatch for %s", action.TrainID)
		}
		var train model.TrainV2
		if err := decodeStrict(raw, &train); err != nil {
			return nil, fmt.Errorf("decode legacy Train %s: %w", action.TrainID, err)
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return nil, err
		}
		plan := trainV2LegacyMigrationPlan{
			action:    action,
			trainPath: trainPath,
			trainRaw:  raw,
			train:     train,
			record:    model.TrainV2LegacyStateMigrationRecord{Action: action.Action, TrainID: train.ID, TrainPath: trainPath, TrainSHA256: action.TrainSHA256, OriginalTrainJSONB64: encodeBytes(raw)},
		}
		switch action.Action {
		case TrainV2LegacyActionMarkHistorical:
			if train.Historical != nil || (train.Status != model.TrainV2RecoveryQuarantined && train.Status != model.TrainV2Retired) {
				return nil, fmt.Errorf("Train %s is not proven legacy historical state", train.ID)
			}
		case TrainV2LegacyActionRetireStale:
			if !staticTrainV2SafeToRetire(train) {
				return nil, fmt.Errorf("Train %s is not safely stale", train.ID)
			}
			live, liveErr := s.trainV2HasLiveOperationWithContext(ctx, in.ProjectID, train.ID)
			if liveErr != nil {
				return nil, liveErr
			}
			if live {
				return nil, fmt.Errorf("Train %s has a live operation", train.ID)
			}
		case TrainV2LegacyActionRecoverIntegrate:
			if !validSHA256(action.IntegrationSHA256) {
				return nil, fmt.Errorf("integration digest is required for %s", train.ID)
			}
			if model.ValidateObjectIdentifier(action.IntegrationMutationID) != nil || !validSHA256(action.IntegrationMutationSHA256) {
				return nil, fmt.Errorf("exact failed integration mutation identity is required for %s", train.ID)
			}
			opPath := trainV2IntegrationOperationPath(in.ProjectID, train.ID)
			opRaw, readErr := s.Hub.ReadFile(ctx, opPath)
			if readErr != nil {
				return nil, readErr
			}
			if digestBytes(opRaw) != action.IntegrationSHA256 {
				return nil, fmt.Errorf("integration operation digest mismatch for %s", train.ID)
			}
			var op trainv2.IntegrationOperation
			if err := decodeStrict(opRaw, &op); err != nil {
				return nil, err
			}
			if err := trainv2.ValidateIntegrationOperation(op); err != nil {
				return nil, err
			}
			if op.Phase != trainv2.IntegrationPhasePrePending {
				return nil, fmt.Errorf("integration operation for %s is not pre_pending", train.ID)
			}
			mutationPath := durableMutationPath(s.Config.StateDir, action.IntegrationMutationID)
			mutationRecordPath := filepath.ToSlash(filepath.Join("operations", "mutations", action.IntegrationMutationID+".json"))
			mutationRaw, mutationErr := os.ReadFile(mutationPath)
			if mutationErr != nil {
				return nil, fmt.Errorf("read exact failed integration mutation for %s: %w", train.ID, mutationErr)
			}
			if digestBytes(mutationRaw) != action.IntegrationMutationSHA256 {
				return nil, fmt.Errorf("failed integration mutation digest mismatch for %s", train.ID)
			}
			var mutation durableMutationOperation
			if err := decodeStrict(mutationRaw, &mutation); err != nil {
				return nil, err
			}
			if mutation.OperationID != action.IntegrationMutationID || mutation.Kind != "train-v2-integrate" || mutation.ProjectID != in.ProjectID || mutation.Status != "failed" {
				return nil, fmt.Errorf("exact integration mutation is not a failed train/integrate operation")
			}
			var mutationInput TrainV2IntegrateInput
			if err := decodeStrict(mutation.Input, &mutationInput); err != nil || mutationInput.ProjectID != in.ProjectID || mutationInput.TrainID != train.ID {
				return nil, fmt.Errorf("exact integration mutation identity does not match %s", train.ID)
			}
			plan.opPath, plan.opRaw, plan.op = opPath, opRaw, op
			plan.mutationPath, plan.mutationRaw, plan.mutation = mutationPath, mutationRaw, mutation
			plan.record.IntegrationPath, plan.record.IntegrationSHA256, plan.record.OriginalIntegrationJSONB64 = opPath, action.IntegrationSHA256, encodeBytes(opRaw)
			plan.record.MutationPath, plan.record.MutationSHA256, plan.record.OriginalMutationJSONB64 = mutationRecordPath, action.IntegrationMutationSHA256, encodeBytes(mutationRaw)
		}
		if err := model.ValidateTrainV2LegacyStateMigrationRecord(plan.record); err != nil {
			return nil, err
		}
		_ = reason
		plans = append(plans, plan)
	}
	return plans, nil
}
func (s *Service) applyLegacyMigrationPlan(worktree, projectID string, plan trainV2LegacyMigrationPlan, reason string, now time.Time) error {
	currentRaw, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(plan.trainPath)))
	if err != nil || digestBytes(currentRaw) != plan.action.TrainSHA256 {
		return fmt.Errorf("legacy Train source changed before migration: %s", plan.trainPath)
	}
	var current model.TrainV2
	if err := decodeStrict(currentRaw, &current); err != nil {
		return err
	}
	switch plan.action.Action {
	case TrainV2LegacyActionMarkHistorical:
		current.Historical = &model.TrainV2HistoricalDisposition{Kind: model.TrainV2HistoricalDispositionKind, SourcePath: plan.trainPath, SourceSHA256: plan.action.TrainSHA256, Reason: reason, MarkedAt: now}
		current.Revision++
		current.UpdatedAt = now
	case TrainV2LegacyActionRetireStale:
		if !staticTrainV2SafeToRetire(current) {
			return fmt.Errorf("Train %s became active before migration", current.ID)
		}
		live, liveErr := s.trainV2HasLiveOperationInWorktree(projectID, current.ID, worktree)
		if liveErr != nil || live {
			if liveErr != nil {
				return liveErr
			}
			return fmt.Errorf("Train %s became active before migration", current.ID)
		}
		previous := current.Status
		current.Status = model.TrainV2Retired
		current.Revision++
		current.UpdatedAt = now
		current.Retirement = &model.TrainV2Retirement{PreviousStatus: previous, Classification: trainV2ClassStale, Reason: reason, ActorSessionID: "state-migration", RetiredAt: now}
	case TrainV2LegacyActionRecoverIntegrate:
		mutationRaw, mutationErr := os.ReadFile(plan.mutationPath)
		if mutationErr != nil || digestBytes(mutationRaw) != plan.action.IntegrationMutationSHA256 {
			return fmt.Errorf("failed integration mutation changed before migration: %s", plan.mutationPath)
		}
		var mutation durableMutationOperation
		if err := decodeStrict(mutationRaw, &mutation); err != nil || mutation.OperationID != plan.action.IntegrationMutationID || mutation.Status != "failed" || mutation.Kind != "train-v2-integrate" || mutation.ProjectID != projectID {
			return fmt.Errorf("failed integration mutation identity changed before migration")
		}
		currentOpRaw, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(plan.opPath)))
		if readErr != nil || digestBytes(currentOpRaw) != plan.action.IntegrationSHA256 {
			return fmt.Errorf("integration operation changed before migration: %s", plan.opPath)
		}
		var currentOp trainv2.IntegrationOperation
		if err := decodeStrict(currentOpRaw, &currentOp); err != nil {
			return err
		}
		if currentOp.Phase != trainv2.IntegrationPhasePrePending {
			return fmt.Errorf("integration operation %s is no longer pre_pending", currentOp.OperationID)
		}
		historyPath := trainV2IntegrationOperationHistoryPath(projectID, current.ID, currentOp.OperationID)
		historyAbs := filepath.Join(worktree, filepath.FromSlash(historyPath))
		if historyRaw, historyErr := os.ReadFile(historyAbs); historyErr == nil {
			if string(historyRaw) != string(currentOpRaw) {
				return fmt.Errorf("integration history identity mismatch for %s", currentOp.OperationID)
			}
		} else if !os.IsNotExist(historyErr) {
			return historyErr
		} else if err := hub.WriteText(worktree, historyPath, string(currentOpRaw)); err != nil {
			return err
		}
		currentOp.Phase = trainv2.IntegrationPhaseRecoveryRequired
		currentOp.RecoveryReason = reason
		currentOp.UpdatedAt = now
		if err := trainv2.ValidateIntegrationOperation(currentOp); err != nil {
			return err
		}
		return hub.WriteJSON(worktree, plan.opPath, currentOp)
	}
	if err := model.ValidateTrainV2(current); err != nil {
		return fmt.Errorf("migrated Train validation failed: %w", err)
	}
	return hub.WriteJSON(worktree, plan.trainPath, current)
}
