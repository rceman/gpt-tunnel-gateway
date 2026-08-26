package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func validateTrainV2AdvanceCurrentItem(item model.TrainV2Item, attemptNumber uint64) error {
	if attemptNumber == 0 || attemptNumber > uint64(len(item.Attempts)) || item.SuccessfulAttemptNumber != attemptNumber {
		return fmt.Errorf("current TrainItem Attempt is not successfully finalized")
	}
	attempt := item.Attempts[attemptNumber-1]
	if attempt.Status != model.TrainV2AttemptSucceeded {
		return fmt.Errorf("current TrainItem Attempt is not successfully finalized")
	}
	if item.Status == model.TrainV2ItemFinalized {
		return nil
	}
	if item.Status == model.TrainV2ItemReviewed && item.Proof != nil && item.Review != nil && item.Review.Outcome == model.ReviewOutcomeAccepted && item.Review.ReportID != "" && attempt.ReviewID == item.Review.ReportID {
		return nil
	}
	return fmt.Errorf("current TrainItem Attempt is not successfully finalized")
}
func (s *Service) dispatchNextTrainV2Attempt(ctx context.Context, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt, runtime trainv2.RuntimeBinding, expected string) (trainv2.StartResult, error) {
	if attempt.DispatchedAt != nil {
		return trainv2.StartResult{ItemPosition: item.Position, Attempt: attempt, Runtime: runtime}, nil
	}
	if err := trainv2.DispatchAttempt(ctx, trainv2.StartDependencies{Shared: s.Durability, OperationID: durableMutationOperationID(ctx), Airelay: s.Airelay, StateDir: s.Config.StateDir, SessionOrigin: AgentSessionID(ctx), MaterializePacket: s.materializeTrainV2Packet}, train, item, attempt, runtime, expected); err != nil {
		return trainv2.StartResult{}, err
	}
	var updated model.TrainV2
	var err error
	if s.Durability == nil {
		return trainv2.StartResult{}, fmt.Errorf("Shared Train authority is unavailable")
	}
	updated, err = s.trainV2ReadShared(ctx, train.ProjectID, train.ID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	item = updated.Items[item.Position]
	attempt = item.Attempts[attempt.Number-1]
	return trainv2.StartResult{ItemPosition: item.Position, Attempt: attempt, Runtime: runtime}, nil
}
