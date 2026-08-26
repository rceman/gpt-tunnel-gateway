package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func requireTrainV2Authoring(ctx context.Context, s *Service, projectID string) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	enabled, err := s.trainV2Enabled(ctx, projectID)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("train_v2 task authoring is not active for project %q", projectID)
	}
	return nil
}

func (s *Service) TaskAuthoringRead(ctx context.Context, projectID, taskID string) (model.TaskAuthoring, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return model.TaskAuthoring{}, err
	}
	if s.Durability == nil {
		return model.TaskAuthoring{}, fmt.Errorf("Shared Task authority is unavailable")
	}
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.TaskAuthoring{}, err
	}
	return s.readSharedTask(ctx, projectID, taskID)
}

func (s *Service) TaskAuthoringFind(ctx context.Context, taskID string) (model.TaskAuthoring, error) {
	if err := model.ValidateCanonicalTaskID(taskID); err != nil {
		return model.TaskAuthoring{}, err
	}
	if s.Durability == nil {
		return model.TaskAuthoring{}, fmt.Errorf("Shared Task authority is unavailable")
	}
	return s.readAnySharedTask(ctx, taskID)
}

func (s *Service) TaskAuthoringCreate(ctx context.Context, in TaskAuthoringCreateInput) (model.TaskAuthoring, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	in.ExpectedHubRevision = ""
	operationID, err := sharedTaskAuthoringOperationID(ctx, "task-authoring-create", in)
	if err != nil {
		return model.TaskAuthoring{}, OperationResult{}, err
	}
	return s.taskAuthoringCreateShared(ctx, operationID, in)
}
