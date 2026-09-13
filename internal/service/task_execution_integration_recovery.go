package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// Integration recovery reads the frozen-Task execution state; the prepared
// commit and admitted proof live in the durable operation capture, so a retry
// or restart resumes from the recorded boundary rather than re-deciding.
func (s *Service) readExecutionForIntegration(ctx context.Context, projectID, key string) (model.TaskExecutionState, bool, error) {
	if s.Durability == nil {
		return model.TaskExecutionState{}, false, fmt.Errorf("shared durability is unavailable")
	}
	state, found, err := s.Durability.ReadTaskExecutionState(ctx, projectID, key)
	if err != nil || !found {
		return state, found, err
	}
	if frozenErr := s.validateFrozenTaskExecutionTask(ctx, state); frozenErr != nil {
		return model.TaskExecutionState{}, false, frozenErr
	}
	return state, true, nil
}
