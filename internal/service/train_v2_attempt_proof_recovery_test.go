package service

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestApplyTrainV2AttemptProofPreservesAcceptedReview(t *testing.T) {
	now := time.Date(2026, time.August, 15, 16, 0, 0, 0, time.UTC)
	item := model.TrainV2Item{
		Position:                16,
		TaskID:                  "GTW-TSK273",
		Status:                  model.TrainV2ItemReviewed,
		SuccessfulAttemptNumber: 1,
		Attempts: []model.TrainV2Attempt{{
			Number:     1,
			Status:     model.TrainV2AttemptSucceeded,
			ReviewID:   "GTW-TRN11-ITEM16-ATTEMPT1-REVIEW",
			StartedAt:  now.Add(-time.Hour),
			FinishedAt: &now,
		}},
		Review: &model.TrainV2ItemReview{
			Outcome:    model.ReviewOutcomeAccepted,
			ReportID:   "GTW-TRN11-ITEM16-ATTEMPT1-REVIEW",
			ReviewedAt: now,
		},
	}
	proof := model.TrainV2ImplementationProof{
		CheckpointHead:    "0123456789abcdef0123456789abcdef01234567",
		ImplementationSHA: "0123456789abcdef0123456789abcdef01234567",
		ReportID:          "gpt-tunnel/v1/projects/gpt-tunnel-gateway/train-attempts/GTW-TRN11/item-16/attempt-1/report.json",
		GateResults:       []model.CompletionGateResult{{ID: model.WorkflowGateTest, ExitCode: 0}},
		RecordedAt:        now,
	}
	originalReview := *item.Review
	if err := applyTrainV2AttemptProof(&item, proof); err != nil {
		t.Fatalf("proof attachment rejected: %v", err)
	}
	if item.Proof == nil || item.Proof.ReportID != proof.ReportID || item.Attempts[0].ReportID != proof.ReportID {
		t.Fatalf("proof was not attached exactly: %#v", item)
	}
	if *item.Review != originalReview || item.Status != model.TrainV2ItemReviewed {
		t.Fatalf("accepted review was changed during proof recovery: %#v", item)
	}
}

func TestApplyTrainV2AttemptProofRejectsUnreviewedOrUnsuccessfulItems(t *testing.T) {
	now := time.Date(2026, time.August, 15, 16, 0, 0, 0, time.UTC)
	proof := model.TrainV2ImplementationProof{
		CheckpointHead:    "0123456789abcdef0123456789abcdef01234567",
		ImplementationSHA: "0123456789abcdef0123456789abcdef01234567",
		ReportID:          "report.json",
		GateResults:       []model.CompletionGateResult{{ID: model.WorkflowGateTest, ExitCode: 0}},
		RecordedAt:        now,
	}
	for name, item := range map[string]model.TrainV2Item{
		"missing accepted review": {
			Status:                  model.TrainV2ItemReviewed,
			SuccessfulAttemptNumber: 1,
			Attempts:                []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, StartedAt: now}},
		},
		"unsuccessful attempt": {
			Status:                  model.TrainV2ItemFinalized,
			SuccessfulAttemptNumber: 1,
			Attempts:                []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptFailed, StartedAt: now}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := applyTrainV2AttemptProof(&item, proof); err == nil {
				t.Fatal("invalid proof attachment was accepted")
			}
		})
	}
}
