package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) resolvePlannedTaskTrain(ctx context.Context, projectID, taskID string) (string, error) {
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{
		ProjectID: projectID,
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil {
		return "", err
	}
	trainID := ""
	for _, train := range trains.Trains {
		for _, item := range train.Items {
			if item.TaskID != taskID {
				continue
			}
			if train.Status != model.TrainV2Planned || item.Status != model.TrainV2ItemQueued || len(item.Attempts) != 0 || item.ActiveAttemptNumber != 0 {
				return "", fmt.Errorf("Task is admitted to a non-startable TrainItem")
			}
			if trainID != "" {
				return "", fmt.Errorf("Task has ambiguous planned Train ownership")
			}
			trainID = train.ID
		}
	}
	if trainID == "" {
		return "", errTaskHasNoCurrentAttempt
	}
	return trainID, nil
}
