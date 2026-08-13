package service

import "context"

func (s *Service) TaskTrainPoll(context.Context, TaskTrainPollInput) (TaskTrainStatus, error) {
	return TaskTrainStatus{}, errRunAuthorityRetired
}
