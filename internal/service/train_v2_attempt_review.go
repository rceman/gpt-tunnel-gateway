package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	Hub    hub.TransactionResult      `json:"hub"`
}

func trainV2AttemptReviewPath(projectID, trainID string, position int, attempt uint64) string {
	return hub.ProtocolRoot + fmt.Sprintf("/projects/%s/train-attempts/%s/item-%d/attempt-%d/review.json", projectID, trainID, position, attempt)
}

func (s *Service) TrainV2AttemptReview(ctx context.Context, in TrainV2AttemptReviewInput) (TrainV2AttemptReviewResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil || in.ItemPosition < 0 || in.AttemptNumber == 0 || model.ValidateReviewOutcome(in.Outcome) != nil {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("invalid Train-v2 Attempt review input")
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	if in.ItemPosition >= len(train.Items) {
		return TrainV2AttemptReviewResult{}, fmt.Errorf("Train item position is out of range")
	}
	item := train.Items[in.ItemPosition]
	if item.TaskID == "" || len(item.Attempts) < int(in.AttemptNumber) || item.SuccessfulAttemptNumber != in.AttemptNumber || item.Status != model.TrainV2ItemFinalized {
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
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2AttemptReviewResult{}, err
		}
	}
	path := trainV2AttemptReviewPath(in.ProjectID, in.TrainID, in.ItemPosition, in.AttemptNumber)
	tx, err := s.Hub.Transact(ctx, expected, "gateway: review Train-v2 Attempt", func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != train.Revision || in.ItemPosition >= len(current.Items) {
			return nil, fmt.Errorf("Train changed before Attempt review")
		}
		currentItem := current.Items[in.ItemPosition]
		if currentItem.SuccessfulAttemptNumber != in.AttemptNumber || currentItem.Status != model.TrainV2ItemFinalized || len(currentItem.Attempts) < int(in.AttemptNumber) || currentItem.Attempts[in.AttemptNumber-1].Status != model.TrainV2AttemptSucceeded || currentItem.Attempts[in.AttemptNumber-1].ReviewID != "" {
			return nil, fmt.Errorf("Attempt changed before review")
		}
		currentItem.Attempts[in.AttemptNumber-1].ReviewID = review.ID
		currentItem.Review = &model.TrainV2ItemReview{Outcome: review.Outcome, ReportID: review.ID, ReviewedAt: review.ReviewedAt}
		currentItem.Status = model.TrainV2ItemReviewed
		current.Items[in.ItemPosition] = currentItem
		current.Revision++
		current.UpdatedAt = review.ReviewedAt
		if err := model.ValidateTrainV2(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, review); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(in.ProjectID, in.TrainID), path}, nil
	})
	if err != nil {
		return TrainV2AttemptReviewResult{}, err
	}
	return TrainV2AttemptReviewResult{
		Review: review,
		Hub:    tx,
	}, nil
}
