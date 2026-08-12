package service

import (
	"context"
	"fmt"
)

func rejectPlanMutationAfterTrainV2(ctx context.Context, s *Service, projectID string) error {
	enabled, err := s.trainV2Enabled(ctx, projectID)
	if err != nil {
		return err
	}
	if enabled {
		return fmt.Errorf("PLAN_AUTHORITY_RETIRED: Plan is historical/read-only after train_v2 cutover")
	}
	return nil
}

func rejectLegacyExecutionAfterTrainV2(ctx context.Context, s *Service, projectID string) error {
	enabled, err := s.trainV2Enabled(ctx, projectID)
	if err != nil {
		return err
	}
	if enabled {
		return fmt.Errorf("TRAIN_V2_AUTHORITY: use Task authoring and Train lifecycle; standalone legacy execution is retired")
	}
	return nil
}
