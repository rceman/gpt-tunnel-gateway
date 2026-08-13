package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) WatcherStatus(ctx context.Context, projectID string) (model.WatcherStatus, error) {
	if enabled, enabledErr := s.TrainV2Enabled(ctx, projectID); enabledErr != nil {
		return model.WatcherStatus{}, enabledErr
	} else if enabled {
		return s.watcherStatusTrainV2(ctx, projectID)
	}
	return model.WatcherStatus{}, errRunAuthorityRetired
}
