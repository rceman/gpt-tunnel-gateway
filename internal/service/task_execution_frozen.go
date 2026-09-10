package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) taskAuthoringReadForExecution(ctx context.Context, projectID, key string) (model.TaskAuthoring, error) {
	task, err := s.TaskAuthoringRead(ctx, projectID, key)
	if err != nil {
		return model.TaskAuthoring{}, err
	}
	if task.Status == model.TaskAuthoringArchived || (task.Status != model.TaskAuthoringPlanned && task.Status != model.TaskAuthoringReady) {
		return model.TaskAuthoring{}, fmt.Errorf("Task is not dispatchable in status %q", task.Status)
	}
	hash, err := model.HashTaskAuthoring(task)
	if err != nil || hash != task.RevisionSHA256 {
		return model.TaskAuthoring{}, fmt.Errorf("Task authoring revision hash is invalid")
	}
	return task, nil
}

// validateFrozenTaskExecutionTask is shared by every execution mutation.
func (s *Service) validateFrozenTaskExecutionTask(ctx context.Context, state model.TaskExecutionState) error {
	task, err := s.TaskLifecycleRead(ctx, state.ProjectID, state.TaskID, state.TaskRevision)
	if err != nil {
		return err
	}
	if task.Revision != state.TaskRevision || task.RevisionSHA256 != state.TaskRevisionSHA256 {
		return fmt.Errorf("Task authoring revision is not the frozen execution revision")
	}
	current, err := s.TaskAuthoringRead(ctx, state.ProjectID, state.TaskID)
	if err != nil {
		return err
	}
	if current.Revision != state.TaskRevision || current.RevisionSHA256 != state.TaskRevisionSHA256 {
		return fmt.Errorf("Task authoring revision changed during execution")
	}
	return nil
}

func (s *Service) readExecutionForMutation(ctx context.Context, projectID, key string) (model.TaskExecutionState, bool, error) {
	if s.Durability == nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("shared durability is unavailable")
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, key)
	if err != nil || !found {
		return state, found, err
	}
	if err := s.validateFrozenTaskExecutionTask(ctx, state); err != nil {
		return model.TaskExecutionState{}, false, err
	}
	return state, true, nil
}
