package service

import (
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

func TestCorrectionPendingAllowsMultipleRejectedReviewsWithAcceptedHistory(t *testing.T) {
	now := time.Now().UTC()
	train := correctionPendingTrainFixture(now)
	accepted := train.Items[1]
	queued := train.Items[1]
	accepted.TaskID = "EXM-TSK337-accepted"
	accepted.Status = model.TrainV2ItemReviewed
	accepted.SuccessfulAttemptNumber = 1
	accepted.Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: "EXM-TRN336-ITEM1-ATTEMPT1-REVIEW", ReviewedAt: now}
	accepted.Attempts = []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, ReviewID: accepted.Review.ReportID, StartedAt: now.Add(-time.Hour)}}
	train.Items[1] = accepted
	secondRejected := train.Items[0]
	secondRejected.Position = 2
	secondRejected.TaskID = "EXM-TSK338"
	secondRejected.Attempts = append([]model.TrainV2Attempt(nil), secondRejected.Attempts...)
	secondRejected.Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeRejectedCorrection, ReportID: "EXM-TRN336-ITEM2-ATTEMPT1-REVIEW", ReviewedAt: now}
	secondRejected.Attempts[0].ReviewID = secondRejected.Review.ReportID
	train.Items = []model.TrainV2Item{train.Items[0], accepted, secondRejected, queued}
	for position := range train.Items {
		train.Items[position].Position = position
	}
	if position, ok := correctionPendingTrain(train); !ok || position != 2 {
		t.Fatalf("multiple rejected reviews were not correction-pending: position=%d ok=%t", position, ok)
	}
}

func TestCorrectionPendingRejectsMalformedRejectedHistory(t *testing.T) {
	train := correctionPendingTrainFixture(time.Now().UTC())
	train.Items = append(train.Items, model.TrainV2Item{Position: 2, TaskID: "EXM-TSK338", Status: model.TrainV2ItemQueued})
	train.Items[0].Attempts[0].ReviewID = "tampered-review"
	if _, ok := correctionPendingTrain(train); ok {
		t.Fatal("malformed rejected history was classified as correction-pending")
	}
}
