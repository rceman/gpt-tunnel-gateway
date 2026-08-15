package service

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2AdvanceAllowsReviewedProofAfterAcceptedReview(t *testing.T) {
	now := time.Now().UTC()
	item := model.TrainV2Item{
		Position:                0,
		TaskID:                  "GTW-TSK274",
		Status:                  model.TrainV2ItemReviewed,
		AddedAt:                 now,
		SuccessfulAttemptNumber: 1,
		Proof: &model.TrainV2ImplementationProof{
			CheckpointHead:    strings.Repeat("a", 40),
			ImplementationSHA: strings.Repeat("b", 40),
			ReportID:          "implementation-report",
			RecordedAt:        now,
		},
		Review: &model.TrainV2ItemReview{
			Outcome:    model.ReviewOutcomeAccepted,
			ReportID:   "review-report",
			ReviewedAt: now,
		},
		Attempts: []model.TrainV2Attempt{{
			Number:    1,
			Status:    model.TrainV2AttemptSucceeded,
			ReviewID:  "review-report",
			StartHead: strings.Repeat("c", 40),
			StartedAt: now,
		}},
	}
	if err := validateTrainV2AdvanceCurrentItem(item, 1); err != nil {
		t.Fatalf("reviewed item with proof should advance: %v", err)
	}

	item.Review.ReportID = "different-review"
	if err := validateTrainV2AdvanceCurrentItem(item, 1); err == nil {
		t.Fatal("review/Attempt mismatch should fail closed")
	}
}
