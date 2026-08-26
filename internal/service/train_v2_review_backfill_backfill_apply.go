package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) TrainV2ReviewBackfill(ctx context.Context, in TrainV2ReviewBackfillInput) (TrainV2ReviewBackfillResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	if err := validateTrainV2ReviewBackfillInput(in); err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	checkRevision, err := s.hubRevision(ctx)
	if err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != checkRevision {
		return TrainV2ReviewBackfillResult{}, fmt.Errorf("HUB_REVISION_CONFLICT: expected %s, got %s", in.ExpectedHubRevision, checkRevision)
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2ReviewBackfillResult{}, err
	}
	receiptPath := trainV2ReviewBackfillPath(in.ProjectID, in.TrainID)
	result := TrainV2ReviewBackfillResult{
		DryRun:      !in.Apply,
		ProjectID:   in.ProjectID,
		TrainID:     in.TrainID,
		ItemStart:   in.ItemStart,
		ItemEnd:     in.ItemEnd,
		HubBefore:   checkRevision,
		ReceiptPath: receiptPath,
	}
	evidence, err := s.sharedTrainEvidence()
	if err != nil {
		return result, err
	}
	if s.Replica == nil {
		return result, fmt.Errorf("replica persistence is unavailable")
	}
	if raw, readErr := s.Replica.ReadFile(ctx, receiptPath); readErr == nil {
		var receipt trainV2ReviewBackfillHubReceipt
		if err := decodeStrict(raw, &receipt); err != nil || receipt.ProjectID != in.ProjectID || receipt.TrainID != in.TrainID || receipt.ItemStart != in.ItemStart || receipt.ItemEnd != in.ItemEnd {
			return result, fmt.Errorf("invalid Train review backfill receipt")
		}
		if err := s.validateAppliedReviewBackfill(train, receipt.Items, evidence); err != nil {
			return result, err
		}
		if receipt.State == "completed" {
			result.AlreadyMigrated, result.Applied, result.HubAfter, result.Items = true, true, receipt.HubAfter, append([]TrainV2ReviewBackfillItem{}, receipt.Items...)
			return result, nil
		}
		if receipt.State != "pending" || !in.Apply {
			return result, fmt.Errorf("invalid pending Train review backfill state")
		}
		result.AlreadyMigrated, result.Applied, result.HubAfter, result.Items = true, true, checkRevision, append([]TrainV2ReviewBackfillItem{}, receipt.Items...)
		return result, nil
	} else if !IsNotFound(readErr) {
		return result, readErr
	}
	items, err := buildTrainV2ReviewBackfillPlan(train, in.ItemStart, in.ItemEnd, evidence)
	if err != nil {
		return result, err
	}
	result.Items = items
	if !in.Apply {
		return result, nil
	}
	now := nowUTC()
	receipt := trainV2ReviewBackfillHubReceipt{
		SchemaVersion: model.TrainV2AttemptSchemaVersion,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		ItemStart:     in.ItemStart,
		ItemEnd:       in.ItemEnd,
		State:         "pending",
		HubBefore:     checkRevision,
		Items:         items,
		Reason:        "backfill accepted reviews for immutable pre-review Train attempts",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	tx, err := s.Hub.Transact(ctx, checkRevision, "gateway: backfill Train-v2 reviews", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision {
			return nil, fmt.Errorf("Train changed before review backfill")
		}
		planned, err := buildTrainV2ReviewBackfillPlan(latest, in.ItemStart, in.ItemEnd, evidence)
		if err != nil {
			return nil, err
		}
		if err := applyTrainV2ReviewBackfill(&latest, planned, now); err != nil {
			return nil, err
		}
		receipt.Items = planned
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		paths := []string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}
		return paths, nil
	})
	if err != nil {
		return result, err
	}
	for _, item := range items {
		review := model.TrainV2AttemptReview{SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", in.TrainID, item.Position, item.AttemptNumber), TrainID: in.TrainID, TaskID: item.TaskID, ItemPosition: item.Position, AttemptNumber: item.AttemptNumber, Outcome: model.ReviewOutcomeAccepted, ReviewedHead: item.ReviewedHead, Findings: []model.ReviewFinding{}, ScopeCoverage: []model.ReviewScopeCoverage{}, ReviewedAt: now}
		if _, err := evidence.WriteAttemptReview(review); err != nil {
			return result, fmt.Errorf("persist backfill review evidence for %s: %w", item.TaskID, err)
		}
	}
	receipt.State, receipt.HubAfter, receipt.UpdatedAt = "completed", tx.After, nowUTC()
	if _, err := s.Hub.Transact(ctx, tx.After, "gateway: complete Train-v2 review backfill", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{receiptPath}, nil
	}); err != nil {
		return result, err
	}
	result.Applied, result.HubAfter, result.ChangedPaths = true, receipt.HubAfter, append([]string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}, reviewPaths(items)...)
	return result, nil
}
func applyTrainV2ReviewBackfill(train *model.TrainV2, items []TrainV2ReviewBackfillItem, now time.Time) error {
	for _, planned := range items {
		item := &train.Items[planned.Position]
		item.Attempts[planned.AttemptNumber-1].ReviewID = fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", train.ID, planned.Position, planned.AttemptNumber)
		item.Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: item.Attempts[planned.AttemptNumber-1].ReviewID, ReviewedAt: now}
		item.Status = model.TrainV2ItemReviewed
	}
	train.Revision++
	train.UpdatedAt = now
	return model.ValidateTrainV2(*train)
}
func (s *Service) validateAppliedReviewBackfill(train model.TrainV2, items []TrainV2ReviewBackfillItem, evidence trainv2.EvidenceStore) error {
	for _, planned := range items {
		if planned.Position < 0 || planned.Position >= len(train.Items) {
			return fmt.Errorf("Train review backfill receipt no longer matches item %d", planned.Position)
		}
		item := train.Items[planned.Position]
		reviewID := fmt.Sprintf("%s-ITEM%d-ATTEMPT%d-REVIEW", train.ID, planned.Position, planned.AttemptNumber)
		if item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted || item.Review.ReportID != reviewID || item.SuccessfulAttemptNumber != planned.AttemptNumber || len(item.Attempts) < int(planned.AttemptNumber) || item.Attempts[planned.AttemptNumber-1].ReviewID != reviewID {
			return fmt.Errorf("Train review backfill receipt no longer matches item %d", planned.Position)
		}
		raw, err := evidence.ReadAttemptReportBytes(train.ID, planned.TaskID, planned.AttemptNumber)
		if err != nil || digestBytes(raw) != planned.ReportSHA256 {
			return fmt.Errorf("Train review backfill report digest changed for item %d", planned.Position)
		}
		var report model.TrainV2AttemptReport
		if err := decodeStrict(raw, &report); err != nil || report.TrainID != train.ID || report.TaskID != planned.TaskID || report.ItemPosition != planned.Position || report.AttemptNumber != planned.AttemptNumber || report.Repository.Head != planned.ReviewedHead || report.Status != "succeeded" {
			return fmt.Errorf("Train review backfill report no longer matches item %d", planned.Position)
		}
		review, err := evidence.ReadAttemptReview(train.ID, planned.TaskID, planned.AttemptNumber)
		if err != nil {
			return fmt.Errorf("Train review backfill review is missing for item %d", planned.Position)
		}
		if review.ID != reviewID || review.TrainID != train.ID || review.TaskID != planned.TaskID || review.ItemPosition != planned.Position || review.AttemptNumber != planned.AttemptNumber || review.Outcome != model.ReviewOutcomeAccepted || review.ReviewedHead != planned.ReviewedHead {
			return fmt.Errorf("Train review backfill review no longer matches item %d", planned.Position)
		}
	}
	return nil
}
func reviewPaths(items []TrainV2ReviewBackfillItem) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.ReviewPath)
	}
	return paths
}
