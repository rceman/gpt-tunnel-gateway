package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) trainV2IntegrationMutationStatus(projectID, trainID string) (string, bool, error) {
	root := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	status := ""
	updated := time.Time{}
	found := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, err := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return "", false, err
		}
		if operation.Kind != "train-v2-integrate" || operation.ProjectID != projectID {
			continue
		}
		var identity struct {
			TrainID string `json:"train_id"`
		}
		if err := json.Unmarshal(operation.Input, &identity); err != nil || identity.TrainID == "" {
			return "", false, fmt.Errorf("integration operation %q has invalid train identity", operation.OperationID)
		}
		if identity.TrainID != trainID || (found && !operation.UpdatedAt.After(updated)) {
			continue
		}
		status, updated, found = operation.Status, operation.UpdatedAt, true
	}
	return status, found, nil
}
func (s *Service) trainV2StaleIntegrationHistory(ctx context.Context, projectID, trainID string) (bool, error) {
	integration, err := s.readIntegrationOperation(ctx, projectID, trainID)
	if err != nil {
		if IsNotFound(err) || os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if integration.Phase != trainv2.IntegrationPhasePrePending {
		return false, nil
	}
	status, found, err := s.trainV2IntegrationMutationStatus(projectID, trainID)
	return found && status == "failed", err
}
func (s *Service) TrainV2Retire(ctx context.Context, in TrainV2RetireInput) (TrainV2RetireResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2RetireResult{}, err
	}
	if model.ValidateProjectIdentifier(in.ProjectID) != nil {
		return TrainV2RetireResult{}, fmt.Errorf("invalid project identifier")
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2RetireResult{}, err
	}
	if strings.TrimSpace(in.Reason) == "" || strings.ContainsAny(in.Reason, "\x00\r\n") || len(in.Reason) > 512 {
		return TrainV2RetireResult{}, fmt.Errorf("bounded retirement reason is required")
	}
	actor := AgentSessionID(ctx)
	if actor == "" {
		return TrainV2RetireResult{}, fmt.Errorf("retirement requires a bound Agent session")
	}
	current, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2RetireResult{}, err
	}
	if current.Status == model.TrainV2Retired {
		return TrainV2RetireResult{
			Train:          current,
			PreviousStatus: current.Retirement.PreviousStatus,
			Classification: current.Retirement.Classification,
			Status:         current.Status,
		}, nil
	}
	classification, err := s.classifyTrainV2Lifecycle(in.ProjectID, current)
	if err != nil {
		return TrainV2RetireResult{}, err
	}
	if !classification.SafeToRetire {
		return TrainV2RetireResult{}, fmt.Errorf("Train cannot be retired: %s", classification.Blocker)
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2RetireResult{}, err
		}
	}
	now := s.durableNow()
	var retired model.TrainV2
	_, err = s.Hub.Transact(ctx, expected, "gateway: retire stale Train "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != current.Revision || latest.Status != current.Status {
			return nil, fmt.Errorf("Train changed before retirement")
		}
		if latest.Status == model.TrainV2Retired {
			retired = latest
			return nil, fmt.Errorf("Train is already retired")
		}
		if !staticTrainV2SafeToRetire(latest) {
			return nil, fmt.Errorf("Train became active before retirement")
		}
		if live, liveErr := s.trainV2HasLiveOperationInWorktree(in.ProjectID, in.TrainID, worktree); liveErr != nil {
			return nil, liveErr
		} else if live {
			return nil, fmt.Errorf("Train became active before retirement")
		}
		latest.Status = model.TrainV2Retired
		latest.Revision++
		latest.UpdatedAt = now
		latest.Retirement = &model.TrainV2Retirement{PreviousStatus: current.Status, Classification: classification.Class, Reason: in.Reason, ActorSessionID: actor, RetiredAt: now}
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		retired = latest
		return []string{s.trainV2Path(in.ProjectID, in.TrainID)}, nil
	})
	if err != nil {
		return TrainV2RetireResult{}, err
	}
	return TrainV2RetireResult{
		Train:          retired,
		PreviousStatus: current.Status,
		Classification: classification.Class,
		Status:         retired.Status,
		OperationID:    "",
	}, nil
}
func (s *Service) TrainV2Reconcile(ctx context.Context, in TrainV2ReconcileInput) (TrainV2ReconcileResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2ReconcileResult{}, err
	}
	if model.ValidateProjectIdentifier(in.ProjectID) != nil {
		return TrainV2ReconcileResult{}, fmt.Errorf("invalid project identifier")
	}
	if in.Apply && AgentSessionID(ctx) == "" {
		return TrainV2ReconcileResult{}, fmt.Errorf("reconciliation apply requires a bound Agent session")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "server-owned stale Train reconciliation"
	}
	if len(reason) > 512 || strings.ContainsAny(reason, "\x00\r\n") {
		return TrainV2ReconcileResult{}, fmt.Errorf("bounded reconciliation reason is invalid")
	}
	trains, err := s.readTrainV2Records(ctx, in.ProjectID)
	if err != nil {
		return TrainV2ReconcileResult{}, err
	}
	result := TrainV2ReconcileResult{
		ProjectID: in.ProjectID,
		DryRun:    !in.Apply,
		Records:   make([]TrainV2ReconcileRecord, 0, len(trains)),
	}
	for _, train := range trains {
		classification, err := s.classifyTrainV2Lifecycle(in.ProjectID, train)
		if err != nil {
			return TrainV2ReconcileResult{}, err
		}
		result.Records = append(result.Records, TrainV2ReconcileRecord{
			TrainID:               train.ID,
			Status:                train.Status,
			Classification:        classification.Class,
			SafeToRetire:          classification.SafeToRetire,
			Blocker:               classification.Blocker,
			RecommendedNextAction: classification.Recommended,
		})
	}
	if !in.Apply {
		return result, nil
	}
	safeCount := 0
	for _, record := range result.Records {
		if record.SafeToRetire {
			safeCount++
		}
	}
	if safeCount == 0 {
		result.Hub = OperationResult{
			ProjectID: in.ProjectID,
			Status:    "no_changes",
		}
		return result, nil
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2ReconcileResult{}, err
		}
	}
	actor := AgentSessionID(ctx)
	now := s.durableNow()
	tx, err := s.Hub.Transact(ctx, expected, "gateway: reconcile stale Trains", func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(trains))
		for i := range trains {
			path := s.trainV2Path(in.ProjectID, trains[i].ID)
			var latest model.TrainV2
			if err := readWorktreeJSON(worktree, path, &latest); err != nil {
				return nil, err
			}
			if latest.Revision != trains[i].Revision || latest.Status != trains[i].Status {
				return nil, fmt.Errorf("Train %s changed before reconciliation", latest.ID)
			}
			if !staticTrainV2SafeToRetire(latest) {
				continue
			}
			if live, liveErr := s.trainV2HasLiveOperationInWorktree(in.ProjectID, latest.ID, worktree); liveErr != nil {
				return nil, liveErr
			} else if live {
				return nil, fmt.Errorf("Train %s became active before reconciliation", latest.ID)
			}
			latest.Status = model.TrainV2Retired
			latest.Revision++
			latest.UpdatedAt = now
			latest.Retirement = &model.TrainV2Retirement{PreviousStatus: trains[i].Status, Classification: trainV2ClassStale, Reason: reason, ActorSessionID: actor, RetiredAt: now}
			if err := model.ValidateTrainV2(latest); err != nil {
				return nil, err
			}
			if err := hub.WriteJSON(worktree, path, latest); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("no stale Train records are safe to reconcile")
		}
		return paths, nil
	})
	if err != nil {
		return TrainV2ReconcileResult{}, err
	}
	for i := range result.Records {
		if result.Records[i].SafeToRetire {
			result.Records[i].Changed = true
			result.Records[i].Status = model.TrainV2Retired
		}
	}
	result.Hub = OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "reconciled",
	}
	return result, nil
}
