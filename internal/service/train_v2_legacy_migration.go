package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const (
	TrainV2LegacyActionMarkHistorical   = "mark_historical"
	TrainV2LegacyActionRetireStale      = "retire_stale"
	TrainV2LegacyActionRecoverIntegrate = "recover_integration"
)

type TrainV2LegacyStateMigrationAction struct {
	Action                    string `json:"action"`
	TrainID                   string `json:"train_id"`
	TrainSHA256               string `json:"train_sha256"`
	IntegrationSHA256         string `json:"integration_sha256,omitempty"`
	IntegrationMutationID     string `json:"integration_mutation_id,omitempty"`
	IntegrationMutationSHA256 string `json:"integration_mutation_sha256,omitempty"`
}

type TrainV2LegacyStateMigrationInput struct {
	ProjectID           string                              `json:"project_id"`
	Actions             []TrainV2LegacyStateMigrationAction `json:"actions"`
	Apply               bool                                `json:"apply"`
	ExpectedHubRevision string                              `json:"expected_hub_revision,omitempty"`
	Reason              string                              `json:"reason"`
}

type TrainV2LegacyStateMigrationResult struct {
	DryRun      bool                                      `json:"dry_run"`
	Applied     bool                                      `json:"applied"`
	AlreadyDone bool                                      `json:"already_done"`
	ProjectID   string                                    `json:"project_id"`
	HubBefore   string                                    `json:"hub_before"`
	HubAfter    string                                    `json:"hub_after"`
	ReceiptPath string                                    `json:"receipt_path"`
	Records     []model.TrainV2LegacyStateMigrationRecord `json:"records"`
}

type trainV2LegacyMigrationPlan struct {
	action       TrainV2LegacyStateMigrationAction
	trainPath    string
	trainRaw     []byte
	train        model.TrainV2
	opPath       string
	opRaw        []byte
	op           trainv2.IntegrationOperation
	mutationPath string
	mutationRaw  []byte
	mutation     durableMutationOperation
	record       model.TrainV2LegacyStateMigrationRecord
}

func trainV2LegacyMigrationReceiptPath(projectID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-legacy-state/receipt.json"
}

func trainV2LegacyMigrationEvidencePath(projectID, trainID, digest string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-legacy-state/records/" + trainID + "-" + digest + ".json"
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (s *Service) TrainV2MigrateLegacyState(ctx context.Context, in TrainV2LegacyStateMigrationInput) (TrainV2LegacyStateMigrationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2LegacyStateMigrationResult{}, err
	}
	if len(in.Actions) == 0 || len(in.Actions) > model.MaxTrainV2Items*128 {
		return TrainV2LegacyStateMigrationResult{}, fmt.Errorf("legacy Train migration requires bounded actions")
	}
	for _, action := range in.Actions {
		if action.Action == TrainV2LegacyActionRecoverIntegrate {
			if err := model.ValidateObjectIdentifier(action.IntegrationMutationID); err != nil {
				return TrainV2LegacyStateMigrationResult{}, fmt.Errorf("invalid integration mutation identity: %w", err)
			}
		}
	}
	if in.Apply && in.ExpectedHubRevision == "" {
		return TrainV2LegacyStateMigrationResult{}, fmt.Errorf("legacy Train migration apply requires expected_hub_revision")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "digest-guarded legacy Train state migration"
	}
	if len(reason) > 512 || strings.ContainsAny(reason, "\x00\r\n") {
		return TrainV2LegacyStateMigrationResult{}, fmt.Errorf("invalid legacy Train migration reason")
	}
	snapshot, err := s.Hub.FreshReadSnapshot(ctx)
	if err != nil {
		return TrainV2LegacyStateMigrationResult{}, err
	}
	readCtx := hub.WithReadSnapshot(ctx, snapshot)
	snapshotClosed := false
	defer func() {
		if !snapshotClosed {
			_ = snapshot.Close()
		}
	}()
	if err := requireTrainV2Authoring(readCtx, s, in.ProjectID); err != nil {
		return TrainV2LegacyStateMigrationResult{}, err
	}
	revision, err := s.hubRevision(readCtx)
	if err != nil {
		return TrainV2LegacyStateMigrationResult{}, err
	}
	result := TrainV2LegacyStateMigrationResult{
		DryRun:      !in.Apply,
		ProjectID:   in.ProjectID,
		HubBefore:   revision,
		ReceiptPath: trainV2LegacyMigrationReceiptPath(in.ProjectID),
		Records:     []model.TrainV2LegacyStateMigrationRecord{},
	}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != revision {
		return result, fmt.Errorf("HUB_REVISION_CONFLICT: expected %s, got %s", in.ExpectedHubRevision, revision)
	}

	if raw, readErr := s.Hub.ReadFile(readCtx, result.ReceiptPath); readErr == nil {
		var receipt model.TrainV2LegacyStateMigrationReceipt
		if err := decodeStrict(raw, &receipt); err != nil || receipt.ProjectID != in.ProjectID || receipt.Reason != reason {
			return result, fmt.Errorf("invalid legacy Train migration receipt")
		}
		if err := model.ValidateTrainV2LegacyStateMigrationReceipt(receipt); err != nil {
			return result, err
		}
		if err := s.verifyLegacyMigrationReceipt(readCtx, receipt, in); err != nil {
			return result, err
		}
		result.AlreadyDone = true
		result.Records = append(result.Records, receipt.Records...)
		if receipt.State == "completed" {
			result.Applied = true
			result.HubAfter = receipt.HubAfter
			return result, nil
		}
		if !in.Apply {
			result.HubAfter = revision
			return result, nil
		}
		completed := receipt
		completed.State = "completed"
		completed.HubAfter = revision
		completed.UpdatedAt = nowUTC()
		tx, txErr := s.Hub.Transact(ctx, revision, "gateway: complete legacy Train migration "+in.ProjectID, func(worktree string) ([]string, error) {
			if err := hub.WriteJSON(worktree, result.ReceiptPath, completed); err != nil {
				return nil, err
			}
			return []string{result.ReceiptPath}, nil
		})
		if txErr != nil {
			return result, txErr
		}
		result.Applied, result.HubAfter = true, tx.After
		return result, nil
	} else if !IsNotFound(readErr) && !os.IsNotExist(readErr) {
		return result, readErr
	}

	plans, err := s.planTrainV2LegacyMigration(readCtx, in, reason)
	if err != nil {
		return result, err
	}
	for _, plan := range plans {
		result.Records = append(result.Records, plan.record)
	}
	if !in.Apply {
		return result, nil
	}
	if err := snapshot.Close(); err != nil {
		return result, err
	}
	snapshotClosed = true
	if _, err := s.Hub.Backup(ctx, "train-v2-legacy-state-migration"); err != nil {
		return result, err
	}
	now := nowUTC()
	pending := model.TrainV2LegacyStateMigrationReceipt{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, State: "pending", HubBefore: revision, HubAfter: revision, Records: append([]model.TrainV2LegacyStateMigrationRecord{}, result.Records...), Reason: reason, CreatedAt: now, UpdatedAt: now}
	tx, err := s.Hub.Transact(ctx, revision, "gateway: migrate legacy Train state "+in.ProjectID, func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(plans)*4+1)
		for _, plan := range plans {
			if err := s.applyLegacyMigrationPlan(worktree, in.ProjectID, plan, reason, now); err != nil {
				return nil, err
			}
			evidencePath := trainV2LegacyMigrationEvidencePath(in.ProjectID, plan.train.ID, plan.record.TrainSHA256)
			if err := hub.WriteJSON(worktree, evidencePath, plan.record); err != nil {
				return nil, err
			}
			paths = append(paths, evidencePath, plan.trainPath)
			if plan.action.Action == TrainV2LegacyActionRecoverIntegrate {
				paths = append(paths, plan.opPath, trainV2IntegrationOperationHistoryPath(in.ProjectID, plan.train.ID, plan.op.OperationID))
			}
		}
		if err := hub.WriteJSON(worktree, result.ReceiptPath, pending); err != nil {
			return nil, err
		}
		return append(paths, result.ReceiptPath), nil
	})
	if err != nil {
		return result, err
	}
	completed := pending
	completed.State, completed.HubAfter, completed.UpdatedAt = "completed", tx.After, nowUTC()
	completionTx, err := s.Hub.Transact(ctx, tx.After, "gateway: complete legacy Train migration "+in.ProjectID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, result.ReceiptPath, completed); err != nil {
			return nil, err
		}
		return []string{result.ReceiptPath}, nil
	})
	if err != nil {
		return result, fmt.Errorf("legacy Train migration applied with incomplete receipt: %w", err)
	}
	result.Applied, result.HubAfter = true, completionTx.After
	return result, nil
}

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
		case TrainV2LegacyActionRetireStale:
			if train.Status != model.TrainV2Retired || train.Retirement == nil {
				return fmt.Errorf("stale Train migration state is incomplete for %s", train.ID)
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
