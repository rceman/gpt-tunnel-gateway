package service

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) TrainV2ReviewResolve(ctx context.Context, in TrainV2ReviewResolveInput) (TrainV2ReviewResolveResult, error) {
	if err := authority.RequirePlanner(ctx); err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil || model.ValidateCanonicalTaskID(in.RejectedTaskID) != nil || in.RejectedItemPosition < 0 || in.RejectedAttemptNumber == 0 || model.ValidateObjectIdentifier(in.RejectedReviewID) != nil || model.ValidateCommitSHA(in.RejectedReviewedHead) != nil || model.ValidateCommitSHA(in.ResolvingHead) != nil || len(in.Corrections) == 0 {
		return TrainV2ReviewResolveResult{}, fmt.Errorf("invalid Train-v2 review resolution input")
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	if in.RejectedItemPosition >= len(train.Items) {
		return TrainV2ReviewResolveResult{}, fmt.Errorf("rejected review item position is out of range")
	}
	rejectedItem := train.Items[in.RejectedItemPosition]
	if rejectedItem.TaskID != in.RejectedTaskID || rejectedItem.Status != model.TrainV2ItemReviewed || rejectedItem.Review == nil || rejectedItem.Review.ReportID != in.RejectedReviewID || rejectedItem.Review.Outcome != model.ReviewOutcomeRejectedCorrection || len(rejectedItem.Attempts) < int(in.RejectedAttemptNumber) {
		return TrainV2ReviewResolveResult{}, fmt.Errorf("rejected review is not the exact correction target")
	}
	rejectedReview, err := s.readAttemptReview(ctx, in.ProjectID, in.TrainID, in.RejectedItemPosition, in.RejectedAttemptNumber)
	if err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	if rejectedReview.ID != in.RejectedReviewID || rejectedReview.Outcome != model.ReviewOutcomeRejectedCorrection || rejectedReview.ReviewedHead != in.RejectedReviewedHead {
		return TrainV2ReviewResolveResult{}, fmt.Errorf("rejected review identity or checkpoint mismatch")
	}
	rejectedFindings := reviewFindingSet(rejectedReview)
	if !sameStringSet(in.FindingIDs, rejectedFindings) {
		return TrainV2ReviewResolveResult{}, fmt.Errorf("resolution finding set does not match rejected review")
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	corrections := make([]model.TrainV2ReviewCorrection, 0, len(in.Corrections))
	covered := map[string]bool{}
	for _, requested := range in.Corrections {
		if requested.ProjectID != in.ProjectID || requested.TrainID != in.TrainID || requested.ItemPosition < 0 || requested.ItemPosition >= len(train.Items) || model.ValidateCanonicalTaskID(requested.TaskID) != nil || requested.AttemptNumber == 0 || model.ValidateObjectIdentifier(requested.ReviewID) != nil {
			return TrainV2ReviewResolveResult{}, fmt.Errorf("correction evidence is not owned by the exact Train")
		}
		item := train.Items[requested.ItemPosition]
		if item.TaskID != requested.TaskID || item.Status != model.TrainV2ItemReviewed || item.Review == nil || item.Review.Outcome != model.ReviewOutcomeAccepted || item.Review.ReportID != requested.ReviewID || item.Proof == nil || len(item.Attempts) < int(requested.AttemptNumber) || item.SuccessfulAttemptNumber != requested.AttemptNumber || item.Attempts[requested.AttemptNumber-1].Status != model.TrainV2AttemptSucceeded || item.Attempts[requested.AttemptNumber-1].ReviewID != requested.ReviewID {
			return TrainV2ReviewResolveResult{}, fmt.Errorf("correction evidence is not an accepted exact Attempt")
		}
		review, err := s.readAttemptReview(ctx, in.ProjectID, in.TrainID, requested.ItemPosition, requested.AttemptNumber)
		if err != nil {
			return TrainV2ReviewResolveResult{}, err
		}
		if review.ID != requested.ReviewID || review.Outcome != model.ReviewOutcomeAccepted || requested.ReviewedHead != review.ReviewedHead || requested.ProofHead != item.Proof.ImplementationSHA {
			return TrainV2ReviewResolveResult{}, fmt.Errorf("correction review/proof identity mismatch")
		}
		if len(requested.FindingIDs) == 0 {
			return TrainV2ReviewResolveResult{}, fmt.Errorf("correction evidence has no finding coverage")
		}
		for _, id := range requested.FindingIDs {
			if !rejectedFindings[id] {
				return TrainV2ReviewResolveResult{}, fmt.Errorf("correction covers an unknown rejected finding")
			}
			covered[id] = true
		}
		ancestor, err := s.Git.IsAncestor(ctx, project.Root, requested.ProofHead, in.ResolvingHead)
		if err != nil || !ancestor {
			if err != nil {
				return TrainV2ReviewResolveResult{}, err
			}
			return TrainV2ReviewResolveResult{}, fmt.Errorf("correction proof is not an ancestor of the resolving head")
		}
		corrections = append(corrections, requested)
	}
	for finding := range rejectedFindings {
		if !covered[finding] {
			return TrainV2ReviewResolveResult{}, fmt.Errorf("rejected finding %q remains unresolved", finding)
		}
	}
	now := time.Now().UTC()
	resolution := model.TrainV2ReviewResolution{
		SchemaVersion:         model.TrainV2AttemptSchemaVersion,
		ID:                    fmt.Sprintf("%s-RESOLUTION-%s", in.TrainID, in.RejectedReviewID),
		ProjectID:             in.ProjectID,
		TrainID:               in.TrainID,
		RejectedTaskID:        in.RejectedTaskID,
		RejectedItemPosition:  in.RejectedItemPosition,
		RejectedAttemptNumber: in.RejectedAttemptNumber,
		RejectedReviewID:      in.RejectedReviewID,
		RejectedReviewedHead:  in.RejectedReviewedHead,
		FindingIDs:            append([]string{}, in.FindingIDs...),
		Corrections:           corrections,
		ResolvingHead:         in.ResolvingHead,
		ReviewerEvidence:      in.ReviewerEvidence,
		RecordedAt:            now,
	}
	if err := model.ValidateTrainV2ReviewResolution(resolution); err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	for _, existing := range train.ReviewResolutions {
		if existing.ID == resolution.ID {
			comparison := resolution
			comparison.RecordedAt = existing.RecordedAt
			if !reflect.DeepEqual(existing, comparison) {
				return TrainV2ReviewResolveResult{}, fmt.Errorf("review resolution identity conflict")
			}
			return TrainV2ReviewResolveResult{
				Train:      train,
				Resolution: existing,
			}, nil
		}
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2ReviewResolveResult{}, err
		}
	}
	updated := train
	updated.ReviewResolutions = append(append([]model.TrainV2ReviewResolution{}, train.ReviewResolutions...), resolution)
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: record Train-v2 review resolution", func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &current); err != nil {
			return nil, err
		}
		for _, existing := range current.ReviewResolutions {
			if existing.ID == resolution.ID {
				comparison := resolution
				comparison.RecordedAt = existing.RecordedAt
				if !reflect.DeepEqual(existing, comparison) {
					return nil, fmt.Errorf("review resolution changed before persistence")
				}
				return nil, nil
			}
		}
		if current.Revision != train.Revision {
			return nil, fmt.Errorf("Train changed before review resolution")
		}
		current.ReviewResolutions = append(current.ReviewResolutions, resolution)
		current.Revision++
		current.UpdatedAt = now
		if err := model.ValidateTrainV2(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), current); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(in.ProjectID, in.TrainID)}, nil
	})
	if err != nil {
		return TrainV2ReviewResolveResult{}, err
	}
	updated.Revision = train.Revision + 1
	return TrainV2ReviewResolveResult{
		Train:      updated,
		Resolution: resolution,
		Hub:        tx,
	}, nil
}
