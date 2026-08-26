package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskAuthoringUpdate(ctx context.Context, in TaskAuthoringUpdateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	in.ExpectedHubRevision = ""
	operationID, err := sharedTaskAuthoringOperationID(ctx, "task-authoring-update", in)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return s.taskAuthoringUpdateShared(ctx, operationID, in)
}

func (s *Service) TaskAuthoringReady(ctx context.Context, in TaskAuthoringReadyInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	in.ExpectedHubRevision = ""
	operationID, err := sharedTaskAuthoringOperationID(ctx, "task-authoring-ready", in)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return s.taskAuthoringReadyShared(ctx, operationID, in)
}

func (s *Service) TaskAuthoringList(ctx context.Context, in TaskAuthoringListInput) (TaskAuthoringListResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TaskAuthoringListResult{}, err
	}
	if s.Durability == nil {
		return TaskAuthoringListResult{}, fmt.Errorf("Shared Task authority is unavailable")
	}
	tasks, err := s.sharedTaskAuthoringAll(ctx, in.ProjectID)
	if err != nil {
		return TaskAuthoringListResult{}, err
	}
	if in.Status != "" {
		filtered := tasks[:0]
		for _, task := range tasks {
			if task.Status == in.Status {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultTaskListLimit
	}
	if limit < 1 || limit > MaxTaskListLimit {
		return TaskAuthoringListResult{}, fmt.Errorf("task authoring list limit must be between 1 and %d", MaxTaskListLimit)
	}
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return TaskAuthoringListResult{Tasks: tasks}, nil
}

func (s *Service) taskAuthoringAll(ctx context.Context, projectID string) ([]model.TaskAuthoring, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return nil, err
	}
	if s.Durability == nil {
		return nil, fmt.Errorf("Shared Task authority is unavailable")
	}
	return s.sharedTaskAuthoringAll(ctx, projectID)
}
