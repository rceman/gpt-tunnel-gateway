package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) readAttemptReview(ctx context.Context, projectID, trainID string, position int, attempt uint64) (model.TrainV2AttemptReview, error) {
	var review model.TrainV2AttemptReview
	if err := s.Hub.ReadJSON(ctx, trainV2AttemptReviewPath(projectID, trainID, position, attempt), &review); err != nil {
		return model.TrainV2AttemptReview{}, err
	}
	if err := model.ValidateTrainV2AttemptReview(review); err != nil {
		return model.TrainV2AttemptReview{}, err
	}
	return review, nil
}
func reviewFindingSet(review model.TrainV2AttemptReview) map[string]bool {
	result := make(map[string]bool, len(review.Findings))
	for _, finding := range review.Findings {
		result[finding.ID] = true
	}
	return result
}
func sameStringSet(ids []string, set map[string]bool) bool {
	if len(ids) != len(set) {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] || !set[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *Service) validateTrainV2ReviewResolutions(ctx context.Context, projectID string, train model.TrainV2, candidate string) error {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return err
	}
	for position, item := range train.Items {
		if item.Review == nil || item.Review.Outcome != model.ReviewOutcomeRejectedCorrection {
			continue
		}
		if item.Status != model.TrainV2ItemReviewed || item.Proof == nil {
			return fmt.Errorf("Train item %q has an unresolved rejected review", item.TaskID)
		}
		review, err := s.readAttemptReview(ctx, projectID, train.ID, position, item.SuccessfulAttemptNumber)
		if err != nil {
			return err
		}
		var found *model.TrainV2ReviewResolution
		for i := range train.ReviewResolutions {
			resolution := &train.ReviewResolutions[i]
			if resolution.RejectedTaskID == item.TaskID && resolution.RejectedItemPosition == position && resolution.RejectedAttemptNumber == item.SuccessfulAttemptNumber && resolution.RejectedReviewID == review.ID {
				if found != nil {
					return fmt.Errorf("multiple resolutions cover rejected Train item %q", item.TaskID)
				}
				found = resolution
			}
		}
		if found == nil || !sameStringSet(found.FindingIDs, reviewFindingSet(review)) {
			return fmt.Errorf("Train item %q has unresolved rejected findings", item.TaskID)
		}
		if found.ProjectID != projectID || found.TrainID != train.ID || found.ResolvingHead == "" {
			return fmt.Errorf("Train item %q has invalid review resolution ownership", item.TaskID)
		}
		resolvedAncestor, err := s.Git.IsAncestor(ctx, project.Root, found.ResolvingHead, candidate)
		if err != nil || !resolvedAncestor {
			if err != nil {
				return err
			}
			return fmt.Errorf("Train item %q resolution is not an ancestor of the full-proof candidate", item.TaskID)
		}
		covered := map[string]bool{}
		for _, correction := range found.Corrections {
			if correction.ProjectID != projectID || correction.TrainID != train.ID || correction.ItemPosition < 0 || correction.ItemPosition >= len(train.Items) {
				return fmt.Errorf("Train item %q resolution crosses project or Train", item.TaskID)
			}
			correctionItem := train.Items[correction.ItemPosition]
			if correctionItem.TaskID != correction.TaskID || correctionItem.Review == nil || correctionItem.Review.Outcome != model.ReviewOutcomeAccepted || correctionItem.Review.ReportID != correction.ReviewID || correctionItem.Proof == nil || correctionItem.Proof.ImplementationSHA != correction.ProofHead || correctionItem.SuccessfulAttemptNumber != correction.AttemptNumber {
				return fmt.Errorf("Train item %q resolution has invalid correction evidence", item.TaskID)
			}
			correctionReview, err := s.readAttemptReview(ctx, projectID, train.ID, correction.ItemPosition, correction.AttemptNumber)
			if err != nil {
				return err
			}
			if correctionReview.ID != correction.ReviewID || correctionReview.Outcome != model.ReviewOutcomeAccepted || correctionReview.ReviewedHead != correction.ReviewedHead {
				return fmt.Errorf("Train item %q resolution has invalid correction review", item.TaskID)
			}
			ancestor, err := s.Git.IsAncestor(ctx, project.Root, correction.ProofHead, candidate)
			if err != nil || !ancestor {
				if err != nil {
					return err
				}
				return fmt.Errorf("Train item %q correction proof is not an ancestor of the candidate", item.TaskID)
			}
			for _, finding := range correction.FindingIDs {
				if covered[finding] {
					return fmt.Errorf("Train item %q finding %q is covered more than once", item.TaskID, finding)
				}
				covered[finding] = true
			}
		}
		if !sameStringSet(found.FindingIDs, covered) {
			return fmt.Errorf("Train item %q resolution leaves findings unresolved", item.TaskID)
		}
	}
	return nil
}
