package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TaskTrain-v1 is retired. Train-v2 execution is addressed by TrainItem
// attempts and is implemented by the train_v2 service surface.
func (s *Service) TaskTrainRead(context.Context, string) (model.TaskTrain, error) {
	return model.TaskTrain{}, errRunAuthorityRetired
}

func (s *Service) TaskTrainReadByID(context.Context, string, string) (model.TaskTrain, error) {
	return model.TaskTrain{}, errRunAuthorityRetired
}

func (s *Service) TaskTrainList(context.Context, string) ([]model.TaskTrain, error) {
	return nil, errRunAuthorityRetired
}
