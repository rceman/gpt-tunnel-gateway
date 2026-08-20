package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func correctionPendingTrainFixture(now time.Time) model.TrainV2 {
	finished := now.Add(-time.Minute)
	return model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion, ID: "EXM-TRN336", ProjectID: "example", Revision: 3,
		Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Items: []model.TrainV2Item{
			{Position: 0, TaskID: "EXM-TSK336", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemReviewed, AddedAt: now.Add(-time.Hour), SuccessfulAttemptNumber: 1, Review: &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeRejectedCorrection, ReportID: "EXM-TRN336-ITEM0-ATTEMPT1-REVIEW", ReviewedAt: finished}, Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "agent-1", AirelaySessionKey: "session-1", GatewayID: "gateway-1", StartHead: strings.Repeat("b", 40), StartedAt: now.Add(-2 * time.Hour), FinishedAt: &finished, ReviewID: "EXM-TRN336-ITEM0-ATTEMPT1-REVIEW"}}},
			{Position: 1, TaskID: "EXM-TSK337", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("c", 64), Status: model.TrainV2ItemQueued, AddedAt: now.Add(-time.Hour)},
		},
	}
}

func TestCorrectionPendingClassificationIsNarrowAndAdvanceStillBlocks(t *testing.T) {
	now := time.Now().UTC()
	train := correctionPendingTrainFixture(now)
	if position, ok := correctionPendingTrain(train); !ok || position != 0 {
		t.Fatalf("rejected review with queued tail was not correction-pending: position=%d ok=%t", position, ok)
	}
	if err := validateTrainV2AdvanceCurrentItem(train.Items[0], 1); err == nil {
		t.Fatal("ordinary Train advance became eligible after rejected review")
	}
	train.Items[1].Status = model.TrainV2ItemRunning
	if _, ok := correctionPendingTrain(train); ok {
		t.Fatal("malformed non-queued correction tail was classified as correction-pending")
	}
	train = correctionPendingTrainFixture(now)
	train.Items[0].Review.ReportID = "other-review"
	if _, ok := correctionPendingTrain(train); ok {
		t.Fatal("mismatched rejected Attempt/review identity was classified as correction-pending")
	}
}

func TestClassifyTrainV2CorrectionPending(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	classification, err := s.classifyTrainV2LifecycleWithContext(context.Background(), "example", correctionPendingTrainFixture(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if classification.Class != trainV2ClassCorrection || classification.Blocker != "TRAIN_CORRECTION_PENDING" {
		t.Fatalf("unexpected correction classification: %#v", classification)
	}
}
