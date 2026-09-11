package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// Integration recovery may finish only after durable post-main evidence and
// an idempotent Task done transition are both present.
func (s *Service) readExecutionForIntegration(ctx context.Context, projectID, key string) (model.TaskExecutionState, bool, error) {
	if s.Durability == nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("shared durability is unavailable")
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, key)
	if err != nil || !found {
		return state, found, err
	}
	if frozenErr := s.validateFrozenTaskExecutionTask(ctx, state); frozenErr == nil {
		return state, true, nil
	} else if durableMutationOperationID(ctx) == "" {
		return model.TaskExecutionState{}, false, frozenErr
	} else {
		operation, readErr := s.readDurableMutation(durableMutationOperationID(ctx))
		capture, captureErr := readTaskExecutionIntegrationCapture(operation)
		if readErr != nil || captureErr != nil || capture.IntegrationHead == "" || capture.ProjectID != projectID || capture.TaskID != key || capture.ExecutionRevision != state.ExecutionRevision || capture.TaskRevision != state.TaskRevision || capture.TaskRevisionSHA256 != state.TaskRevisionSHA256 || capture.Branch != state.Branch || capture.LaneHead != state.Head {
			return model.TaskExecutionState{}, false, frozenErr
		}
		current, currentErr := s.TaskAuthoringRead(ctx, projectID, key)
		if currentErr != nil || current.Status != model.TaskAuthoringDone {
			return model.TaskExecutionState{}, false, frozenErr
		}
		return state, true, nil
	}
}
