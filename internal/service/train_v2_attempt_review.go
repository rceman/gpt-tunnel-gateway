package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2AttemptReviewInput struct {
	ProjectID     string                      `json:"project_id"`
	TrainID       string                      `json:"train_id"`
	ItemPosition  int                         `json:"item_position"`
	AttemptNumber uint64                      `json:"attempt_number"`
	Outcome       string                      `json:"outcome"`
	ReviewedHead  string                      `json:"reviewed_head,omitempty"`
	Findings      []model.ReviewFinding       `json:"findings,omitempty"`
	ScopeCoverage []model.ReviewScopeCoverage `json:"scope_coverage,omitempty"`
	WriteOptions
}

type TrainV2AttemptReviewResult struct {
	Review model.TrainV2AttemptReview `json:"review"`
}

func (s *Service) TrainV2AttemptReview(ctx context.Context, in TrainV2AttemptReviewInput) (TrainV2AttemptReviewResult, error) {
	if s.Durability == nil {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("Shared Train authority is unavailable")
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil || in.ItemPosition < 0 || in.AttemptNumber == 0 || model.ValidateReviewOutcome(in.Outcome) != nil {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("invalid Train-v2 Attempt review input")
	}
	train, err := s.trainV2ReadShared(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	if in.ItemPosition >= len(train.Items) {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("Train item position is out of range")
	}
	item := train.Items[in.ItemPosition]
	if item.TaskID == "" || len(item.Attempts) < int(in.AttemptNumber) || item.SuccessfulAttemptNumber != in.AttemptNumber || item.Status != model.TrainV2ItemFinalized || item.Proof == nil {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("Attempt is not the exact successful review target")
	}
	attempt := item.Attempts[in.AttemptNumber-1]
	if attempt.Status != model.TrainV2AttemptSucceeded || attempt.ReviewID != "" {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("Attempt is not reviewable")
	}
	if in.ReviewedHead == "" {
		in.ReviewedHead = attempt.StartHead
	}
	review := model.TrainV2AttemptReview{SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", in.TrainID, in.ItemPosition, in.AttemptNumber), TrainID: in.TrainID, TaskID: item.TaskID, ItemPosition: in.ItemPosition, AttemptNumber: in.AttemptNumber, Outcome: in.Outcome, ReviewedHead: in.ReviewedHead, Findings: append([]model.ReviewFinding{}, in.Findings...), ScopeCoverage: append([]model.ReviewScopeCoverage{}, in.ScopeCoverage...), ReviewedAt: time.Now().UTC()}
	if err := model.ValidateTrainV2AttemptReview(review); err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	evidence, err := s.sharedTrainEvidence()
	if err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	if _, err := evidence.WriteAttemptReview(review); err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	currentItem := train.Items[in.ItemPosition]
	if currentItem.SuccessfulAttemptNumber != in.AttemptNumber || currentItem.Status != model.TrainV2ItemFinalized || currentItem.Proof == nil || len(currentItem.Attempts) < int(in.AttemptNumber) || currentItem.Attempts[in.AttemptNumber-1].Status != model.TrainV2AttemptSucceeded || currentItem.Attempts[in.AttemptNumber-1].ReviewID != "" {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("Attempt changed before review")
	}
	currentItem.Attempts[in.AttemptNumber-1].ReviewID = review.ID
	currentItem.Review = &model.TrainV2ItemReview{Outcome: review.Outcome, ReportID: review.ID, ReviewedAt: review.ReviewedAt}
	currentItem.Status = model.TrainV2ItemReviewed
	train.Items[in.ItemPosition] = currentItem
	train.Revision++
	train.UpdatedAt = review.ReviewedAt
	if err := model.ValidateTrainV2(train); err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	if err := s.commitSharedTrain(ctx, durableMutationOperationID(ctx), train, "train-attempt-review"); err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	return TrainV2AttemptReviewResult{
		Review: review,
	}, nil
}
