package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestTrainV2ReviewResolveUsesSharedWhenHubUnavailable(t *testing.T) {
	s, revision, projectHead := testService(t)
	_ = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	first, err := trainv2.NewTask("example", "EXM-TSK901", trainv2.AuthoringDraft{
		Title: "Rejected review", Objective: "Preserve rejected review evidence.", ADRRelation: model.TaskADRNoRequired,
	}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	first, err = trainv2.ReadyTask(first, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := trainv2.NewTask("example", "EXM-TSK902", trainv2.AuthoringDraft{
		Title: "Accepted correction", Objective: "Provide accepted correction evidence.", ADRRelation: model.TaskADRNoRequired,
	}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err = trainv2.ReadyTask(second, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	train, err := trainv2.New("example", "EXM-TRN901", "planner", []model.TaskAuthoring{first, second}, now)
	if err != nil {
		t.Fatal(err)
	}
	baseTrain := train
	train.Status = model.TrainV2Running
	rejectedReviewID := "EXM-TRN901-ITEM0-ATTEMPT1-REVIEW"
	acceptedReviewID := "EXM-TRN901-ITEM1-ATTEMPT1-REVIEW"
	finished := now.Add(time.Minute)
	proof := func(reportID string) *model.TrainV2ImplementationProof {
		return &model.TrainV2ImplementationProof{
			CheckpointHead: projectHead, ImplementationSHA: projectHead, ReportID: reportID,
			GateResults: []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}, RecordedAt: finished,
		}
	}
	train.Items[0].Status = model.TrainV2ItemReviewed
	train.Items[0].SuccessfulAttemptNumber = 1
	train.Items[0].Attempts = []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "coder-example", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: projectHead, StartedAt: now, FinishedAt: &finished, ReviewID: rejectedReviewID}}
	train.Items[0].Proof = proof("rejected-report")
	train.Items[0].Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeRejectedCorrection, ReportID: rejectedReviewID, ReviewedAt: finished}
	train.Items[1].Status = model.TrainV2ItemReviewed
	train.Items[1].SuccessfulAttemptNumber = 1
	train.Items[1].Attempts = []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "coder-example", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: projectHead, StartedAt: now, FinishedAt: &finished, ReviewID: acceptedReviewID}}
	train.Items[1].Proof = proof("accepted-report")
	train.Items[1].Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: acceptedReviewID, ReviewedAt: finished}
	train.UpdatedAt = finished
	train.Revision++
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	basePayload, err := json.Marshal(baseTrain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{
		OperationID: "review-resolution-fixture-base", EntityType: "train", EntityID: baseTrain.ID,
		Revision: int64(baseTrain.Revision), Kind: "fixture", Payload: basePayload, Create: true,
	}); err != nil {
		t.Fatal(err)
	}
	s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	if _, err := s.TrainEvidence.WriteAttemptReview(model.TrainV2AttemptReview{
		SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: rejectedReviewID, TrainID: train.ID, TaskID: first.ID,
		ItemPosition: 0, AttemptNumber: 1, Outcome: model.ReviewOutcomeRejectedCorrection, ReviewedHead: projectHead,
		Findings: []model.ReviewFinding{{ID: "F1", Severity: "high", Title: "correction", Detail: "bounded"}}, ReviewedAt: finished,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainEvidence.WriteAttemptReview(model.TrainV2AttemptReview{
		SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: acceptedReviewID, TrainID: train.ID, TaskID: second.ID,
		ItemPosition: 1, AttemptNumber: 1, Outcome: model.ReviewOutcomeAccepted, ReviewedHead: projectHead, ReviewedAt: finished,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.commitSharedTrain(context.Background(), "review-resolution-fixture", train, "fixture"); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = strings.Repeat("/unavailable", 2)
	ctx := WithPlannerWorkflowPolicyAuthority(context.Background())
	result, err := s.TrainV2ReviewResolve(ctx, TrainV2ReviewResolveInput{
		ProjectID: "example", TrainID: train.ID, RejectedTaskID: first.ID, RejectedItemPosition: 0, RejectedAttemptNumber: 1,
		RejectedReviewID: rejectedReviewID, RejectedReviewedHead: projectHead, FindingIDs: []string{"F1"}, ResolvingHead: projectHead,
		ReviewerEvidence: "planner-confirmed", Corrections: []model.TrainV2ReviewCorrection{{
			ProjectID: "example", TrainID: train.ID, TaskID: second.ID, ItemPosition: 1, AttemptNumber: 1,
			ReviewID: acceptedReviewID, ReviewedHead: projectHead, ProofHead: projectHead, FindingIDs: []string{"F1"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolution.TrainID != train.ID || len(result.Train.ReviewResolutions) != 1 {
		t.Fatalf("unexpected Shared review resolution: %#v", result)
	}
}
