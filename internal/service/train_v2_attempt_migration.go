package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2AttemptMigrationInput struct {
	ProjectID           string
	TrainID             string
	ExpectedHubRevision string
	Apply               bool
}

type TrainV2AttemptMigrationResult struct {
	DryRun          bool
	Applied         bool
	AlreadyMigrated bool
	ProjectID       string
	TrainID         string
	HubBefore       string
	HubAfter        string
	ReceiptPath     string
	ChangedPaths    []string
	Items           []model.TrainV2AttemptMigrationItem
}

func trainV2AttemptMigrationPath(projectID, trainID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-attempts/" + trainID + ".json"
}

func (s *Service) TrainV2MigrateAttempts(ctx context.Context, in TrainV2AttemptMigrationInput) (TrainV2AttemptMigrationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptMigrationResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2AttemptMigrationResult{}, err
	}
	if in.Apply && in.ExpectedHubRevision == "" {
		return TrainV2AttemptMigrationResult{}, fmt.Errorf("Train-v2 migration apply requires expected_hub_revision")
	}
	checkRevision, err := s.hubRevision(ctx)
	if err != nil {
		return TrainV2AttemptMigrationResult{}, err
	}
	result := TrainV2AttemptMigrationResult{
		DryRun:       !in.Apply,
		ProjectID:    in.ProjectID,
		TrainID:      in.TrainID,
		HubBefore:    checkRevision,
		ChangedPaths: []string{},
	}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != checkRevision {
		return result, fmt.Errorf("HUB_REVISION_CONFLICT: expected %s, got %s", in.ExpectedHubRevision, checkRevision)
	}
	rawTrain, err := s.Hub.ReadFile(ctx, s.trainV2Path(in.ProjectID, in.TrainID))
	if err != nil {
		return result, err
	}
	train, err := decodeLegacyTrainV2MigrationSource(rawTrain)
	if err != nil {
		return result, err
	}
	if train.ProjectID != in.ProjectID || train.ID != in.TrainID || train.SchemaVersion != model.TrainV2SchemaVersion {
		return result, fmt.Errorf("invalid Train v2 migration source identity")
	}
	receiptPath := trainV2AttemptMigrationPath(in.ProjectID, in.TrainID)
	result.ReceiptPath = receiptPath
	if raw, readErr := s.Hub.ReadFile(ctx, receiptPath); readErr == nil {
		var receipt model.TrainV2AttemptMigrationReceipt
		if err := decodeStrict(raw, &receipt); err != nil {
			return result, fmt.Errorf("invalid Train v2 attempt migration receipt: %w", err)
		}
		if receipt.ProjectID != in.ProjectID || receipt.TrainID != in.TrainID || (receipt.State != "completed" && receipt.State != "pending") {
			return result, fmt.Errorf("invalid Train v2 attempt migration receipt identity")
		}
		if err := validateMigratedTrainItems(train, receipt.Items); err != nil {
			return result, fmt.Errorf("completed Train v2 attempt migration no longer matches: %w", err)
		}
		if receipt.State == "pending" {
			receipt.State = "completed"
			receipt.HubAfter = checkRevision
			if _, txErr := s.Hub.Transact(ctx, checkRevision, "gateway: complete Train v2 attempt migration "+in.TrainID, func(worktree string) ([]string, error) {
				if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
					return nil, err
				}
				return []string{receiptPath}, nil
			}); txErr != nil {
				return result, txErr
			}
		}
		result.AlreadyMigrated = true
		result.Applied = true
		result.HubAfter = checkRevision
		result.Items = append([]model.TrainV2AttemptMigrationItem{}, receipt.Items...)
		return result, nil
	} else if !IsNotFound(readErr) {
		return result, readErr
	}

	items, err := s.buildTrainV2AttemptMigration(ctx, in.ProjectID, train)
	if err != nil {
		return result, err
	}
	result.Items = items
	if !in.Apply {
		return result, nil
	}
	backup, err := s.Hub.Backup(ctx, "train-v2-attempt-migration")
	if err != nil {
		return result, err
	}
	_ = backup
	now := nowUTC()
	receipt := model.TrainV2AttemptMigrationReceipt{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, State: "pending", HubBefore: checkRevision, Items: append([]model.TrainV2AttemptMigrationItem{}, items...), Reason: "migrate Train v2 execution from global Run identity to item-local attempts", CreatedAt: now, UpdatedAt: now}
	tx, err := s.Hub.Transact(ctx, checkRevision, "gateway: migrate Train v2 attempts "+in.TrainID, func(worktree string) ([]string, error) {
		latestRaw, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.trainV2Path(in.ProjectID, in.TrainID))))
		if err != nil {
			return nil, err
		}
		latest, err := decodeLegacyTrainV2MigrationSource(latestRaw)
		if err != nil {
			return nil, err
		}
		if err := s.applyTrainV2AttemptMigration(worktree, &latest, items); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		receipt.HubAfter = "pending"
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		paths := []string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}
		for _, item := range items {
			path := filepath.Join(worktree, filepath.FromSlash(item.LegacyRunRef.Path))
			if !strings.Contains(item.LegacyRunRef.Path, "/runs/") {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			} else if err == nil {
				paths = append(paths, item.LegacyRunRef.Path)
			}
		}
		return paths, nil
	})
	if err != nil {
		return result, err
	}
	receipt.HubAfter = tx.After
	_, err = s.Hub.Transact(ctx, tx.After, "gateway: complete Train v2 attempt migration "+in.TrainID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{receiptPath}, nil
	})
	if err != nil {
		return result, fmt.Errorf("Train v2 attempt migration committed with pending receipt: %w", err)
	}
	result.Applied = true
	result.HubAfter = tx.After
	result.ChangedPaths = []string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}
	return result, nil
}
