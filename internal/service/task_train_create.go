package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskTrainCreate(context.Context, TaskTrainCreateInput) (model.TaskTrain, OperationResult, error) {
	return model.TaskTrain{}, OperationResult{}, errRunAuthorityRetired
}

func (s *Service) TaskTrainStatus(context.Context, string) (TaskTrainStatus, error) {
	return TaskTrainStatus{}, errRunAuthorityRetired
}

func (s *Service) TaskTrainStatusByID(context.Context, string, string) (TaskTrainStatus, error) {
	return TaskTrainStatus{}, errRunAuthorityRetired
}
