package service

import (
	"context"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) trainV2Root(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-trains-v2"
	}
	return s.projectPrefix(projectID) + "/trains-v2"
}

func (s *Service) trainV2Path(projectID, trainID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-train-v2"
	}
	if _, _, err := model.ParseTrainV2ID(trainID); err != nil {
		return "../invalid-train-v2"
	}
	return s.trainV2Root(projectID) + "/" + trainID + ".json"
}

func (s *Service) TrainV2Read(ctx context.Context, projectID, trainID string) (model.TrainV2, error) {
	return s.trainV2ReadShared(ctx, projectID, trainID)
}

func (s *Service) TrainV2List(ctx context.Context, in TrainV2ListInput) (TrainV2ListResult, error) {
	trains, err := s.trainV2ListShared(ctx, in.ProjectID, in.Limit)
	if err != nil {
		return TrainV2ListResult{}, err
	}
	return TrainV2ListResult{Trains: trains}, nil
}
