package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
