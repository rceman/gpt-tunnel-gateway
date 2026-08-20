package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) trainV2HasLiveOperation(projectID, trainID string) (bool, error) {
	return s.trainV2HasLiveOperationWithContext(context.Background(), projectID, trainID)
}
func (s *Service) trainV2HasLiveOperationInWorktree(projectID, trainID, worktree string) (bool, error) {
	return s.trainV2HasLiveOperationInWorktreeContext(context.Background(), projectID, trainID, worktree)
}
func (s *Service) trainV2HasLiveOperationWithContext(ctx context.Context, projectID, trainID string) (bool, error) {
	return s.trainV2HasLiveOperationInWorktreeContext(ctx, projectID, trainID, "")
}
func (s *Service) trainV2HasLiveOperationInWorktreeContext(ctx context.Context, projectID, trainID, worktree string) (bool, error) {
	var integration trainv2.IntegrationOperation
	var integrationErr error
	if worktree == "" {
		integration, integrationErr = s.readIntegrationOperation(ctx, projectID, trainID)
	} else {
		integrationErr = readWorktreeJSON(worktree, trainV2IntegrationOperationPath(projectID, trainID), &integration)
	}
	if integrationErr == nil {
		if integration.Phase != "completed" && integration.Phase != "failed" {
			status, found, err := s.trainV2IntegrationMutationStatus(projectID, trainID)
			if err != nil {
				return false, err
			}
			if !found || status != "failed" {
				return true, nil
			}
			// A failed durable integration mutation can leave the Hub lifecycle
			// at pre_pending. Preserve both records, but do not treat that stale
			// prefix as a live owner of the project integration lane.
		}
	} else if !IsNotFound(integrationErr) && !os.IsNotExist(integrationErr) {
		return false, integrationErr
	}
	root := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, err := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return false, err
		}
		if operation.ProjectID != projectID || operation.Status == "completed" || operation.Status == "failed" {
			continue
		}
		switch operation.Kind {
		case "train-v2-create", "train-v2-retire", "train-v2-reconcile":
			// These are project-level lifecycle mutations or the current
			// retirement operation itself. Hub revision checks serialize their
			// writes; they do not represent an execution Attempt for this Train.
			continue
		case "train-v2-start", "train-v2-advance", "train-attempt-finalize", "train-attempt-review", "train-attempt-proof-recovery", "train-v2-review-backfill", "train-v2-full-proof", "train-v2-integrate", "train-v2-cutover", "train-v2-add":
			var identity struct {
				TrainID string `json:"train_id"`
			}
			if err := json.Unmarshal(operation.Input, &identity); err != nil || identity.TrainID == "" {
				return false, fmt.Errorf("active Train operation %q has invalid train identity", operation.OperationID)
			}
			if identity.TrainID == trainID {
				return true, nil
			}
		default:
			if strings.HasPrefix(operation.Kind, "train-") || strings.HasPrefix(operation.Kind, "train-v2-") {
				return false, fmt.Errorf("active Train operation %q has unknown kind %q", operation.OperationID, operation.Kind)
			}
		}
	}
	return false, nil
}
