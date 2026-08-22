package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
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
		enabled, err := s.trainV2Enabled(ctx, project.ID)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
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

func (s *Service) checkSessionAvailableForTrainAttemptLocalFirst(ctx context.Context, session, trainID string) error {
	if s.Durability == nil {
		return s.checkSessionAvailableForTrainAttempt(ctx, session, trainID)
	}
	for projectID := range s.Config.Projects {
		enabled, err := s.trainV2Enabled(ctx, projectID)
		if err != nil {
			return err
		}
		if !enabled {
			continue
		}
		trains, err := s.sharedTrains(ctx, projectID)
		if err != nil {
			return err
		}
		for _, train := range trains {
			if train.ID == trainID || (train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked) {
				continue
			}
			runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
			if runtimeErr != nil {
				continue
			}
			if runtime.SessionKey == session {
				return fmt.Errorf("Train Attempt %s:%d already owns the project session", train.ID, runtime.AttemptNumber)
			}
		}
	}
	return nil
}
