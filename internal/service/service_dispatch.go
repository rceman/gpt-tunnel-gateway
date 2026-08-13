package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TaskDispatch(context.Context, DispatchInput) (model.TrainV2Attempt, OperationResult, error) {
	return model.TrainV2Attempt{}, OperationResult{}, errRunAuthorityRetired
}

// checkSessionAvailable is retained as a narrow Train-v2 ownership check for
// callers that have not yet been migrated to the explicit helper name. It
// never reads legacy Run records.
func (s *Service) checkSessionAvailable(ctx context.Context, session string) error {
	return s.checkSessionAvailableForTrainAttempt(ctx, session, "")
}

func (s *Service) checkSessionAvailableForTrainAttempt(ctx context.Context, session, trainID string) error {
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		active, found, err := s.trainV2ActiveAttempt(ctx, project.ID)
		if err != nil {
			return err
		}
		if found && active.Train.ID != trainID && active.Attempt.AirelaySessionKey == session {
			return fmt.Errorf("Train Attempt %s:%d already owns the project session", active.Train.ID, active.Attempt.Number)
		}
	}
	return nil
}
