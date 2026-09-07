package service

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) populateProjectOperationalTrain(result *ProjectOperationalStatus, trains []model.TrainV2) {
	sort.Slice(trains, func(i, j int) bool { return trains[i].UpdatedAt.After(trains[j].UpdatedAt) })
	for _, train := range trains {
		if train.Historical != nil {
			continue
		}
		if train.Status == model.TrainV2Completed || train.Status == model.TrainV2ReadyForIntegration || train.Status == model.TrainV2Retired {
			if result.State == "idle" {
				result.RecommendedNextAction = "review or integrate current Train"
			}
			continue
		}
		if position, ok := correctionPendingTrain(train); ok {
			correction := correctionTrainProjection(train, position)
			result.TrainID = correction.TrainID
			result.TrainState = correction.Status
			result.State = "correction_pending"
			result.Blocker = correction.Blocker
			result.RecommendedNextAction = correction.RecommendedNextAction
			return
		}
		result.TrainID, result.TrainState = train.ID, train.Status
		for _, item := range train.Items {
			if item.Status != model.TrainV2ItemRunning && item.Status != model.TrainV2ItemBlocked && item.Status != model.TrainV2ItemQueued {
				continue
			}
			result.TaskID, result.ItemPosition, result.ItemState = item.TaskID, item.Position, item.Status
			if item.ActiveAttemptNumber > 0 && item.ActiveAttemptNumber <= uint64(len(item.Attempts)) {
				result.AttemptNumber = item.ActiveAttemptNumber
				result.AttemptState = item.Attempts[item.ActiveAttemptNumber-1].Status
			}
			if item.Status == model.TrainV2ItemBlocked || train.Status == model.TrainV2Blocked {
				result.State, result.Blocker, result.RecommendedNextAction = "blocked", "train_blocked", "inspect blocker"
			} else if item.Status == model.TrainV2ItemRunning || train.Status == model.TrainV2Running {
				result.State, result.RecommendedNextAction = "working", "supervise current Attempt"
			}
			return
		}
	}
}

func (s *Service) projectOperationalOperation(projectID string) *ProjectOperationalOperation {
	dir := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var candidates []durableMutationOperation
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, readErr := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if readErr != nil || operation.ProjectID != projectID || operation.Status == "completed" || operation.Status == "failed" || operation.Status == "outcome_unknown" {
			continue
		}
		candidates = append(candidates, operation)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt) })
	return &ProjectOperationalOperation{
		Kind:        candidates[0].Kind,
		OperationID: candidates[0].OperationID,
		Status:      candidates[0].Status,
	}
}
