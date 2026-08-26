package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTrainV2CorrectionStartRequiresExactRejectedReviewAndQueuedTask(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	rejectedTask, revision := readyTrainTaskForTest(t, s, revision, "rejected correction source")
	correctionTask, revision := readyTrainTaskForTest(t, s, revision, "queued correction")
	train, created, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{rejectedTask.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	train, added, err := s.TrainV2Add(context.Background(), TrainV2AddInput{
		ProjectID:        "example",
		TrainID:          train.ID,
		TaskIDs:          []string{correctionTask.ID},
		ExpectedRevision: train.Revision,
		AddedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{ID: train.ID, Revision: int64(train.Revision), Payload: payload, UpdatedAt: train.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	seedLocalCodingAgentSessionForTaskWorkTest(t, s)
	ensureTaskWorkMirrorForTest(t, s)
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		AgentID:   "coder-example",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: added.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewID := fmt.Sprintf("%s-ITEM0-ATTEMPT1-REVIEW", train.ID)
	now := nowUTC()
	current, err := s.trainV2ReadShared(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	item := &current.Items[0]
	item.Attempts[0].Status = model.TrainV2AttemptSucceeded
	item.Attempts[0].FinishedAt = &now
	item.Attempts[0].ReviewID = reviewID
	item.SuccessfulAttemptNumber = 1
	item.ActiveAttemptNumber = 0
	item.Status = model.TrainV2ItemReviewed
	item.Proof = &model.TrainV2ImplementationProof{CheckpointHead: strings.Repeat("a", 40), ImplementationSHA: strings.Repeat("b", 40), ReportID: "report-rejected", GateResults: []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}, RecordedAt: now}
	item.Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeRejectedCorrection, ReportID: reviewID, ReviewedAt: now}
	current.Revision++
	current.UpdatedAt = now
	review := model.TrainV2AttemptReview{SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: reviewID, TrainID: train.ID, TaskID: rejectedTask.ID, ItemPosition: 0, AttemptNumber: 1, Outcome: model.ReviewOutcomeRejectedCorrection, ReviewedHead: started.Attempt.StartHead, Findings: []model.ReviewFinding{{ID: "F1", Severity: "high", Title: "needs correction", Detail: "bounded"}}, ScopeCoverage: []model.ReviewScopeCoverage{}, ReviewedAt: now}
	if _, err := s.TrainEvidence.WriteAttemptReview(review); err != nil {
		t.Fatal(err)
	}
	payload, err = json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{OperationID: "test-rejected-review-" + train.ID, EntityType: "train", EntityID: train.ID, ExpectedRevision: int64(current.Revision - 1), Revision: int64(current.Revision), Kind: "test-rejected-review", Payload: payload, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	bad := TrainV2CorrectionStartInput{
		ProjectID:                    "example",
		TrainID:                      train.ID,
		RejectedItemPosition:         0,
		RejectedAttemptNumber:        1,
		RejectedReviewID:             "wrong-review",
		CorrectionItemPosition:       1,
		CorrectionTaskID:             correctionTask.ID,
		CorrectionTaskRevision:       correctionTask.Revision,
		CorrectionTaskRevisionSHA256: correctionTask.RevisionSHA256,
		StartedBy:                    "planner",
		AgentID:                      "coder-example",
	}
	if _, err := s.TrainV2CorrectionStart(context.Background(), bad); err == nil {
		t.Fatal("mismatched rejected review was accepted")
	}
	good := bad
	good.RejectedReviewID = reviewID
	if _, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   train.ID,
	}); err == nil {
		t.Fatal("ordinary advance opened rejected correction")
	}
	wrongTask := bad
	wrongTask.RejectedReviewID, wrongTask.CorrectionTaskID = reviewID, "EXM-TSK999"
	if _, err := s.TrainV2CorrectionStart(context.Background(), wrongTask); err == nil {
		t.Fatal("wrong queued Task identity was accepted")
	}
	wrongRevision := bad
	wrongRevision.RejectedReviewID, wrongRevision.CorrectionTaskRevision = reviewID, correctionTask.Revision+1
	if _, err := s.TrainV2CorrectionStart(context.Background(), wrongRevision); err == nil {
		t.Fatal("wrong queued Task revision was accepted")
	}
	wrongDigest := bad
	wrongDigest.RejectedReviewID, wrongDigest.CorrectionTaskRevisionSHA256 = reviewID, strings.Repeat("d", 64)
	if _, err := s.TrainV2CorrectionStart(context.Background(), wrongDigest); err == nil {
		t.Fatal("wrong queued Task digest was accepted")
	}
	wrongTrain := bad
	wrongTrain.RejectedReviewID, wrongTrain.TrainID = reviewID, "EXM-TRN999"
	if _, err := s.TrainV2CorrectionStart(context.Background(), wrongTrain); err == nil {
		t.Fatal("cross-Train correction was accepted")
	}
	if err := os.WriteFile(started.Runtime.WorktreePath+"/TSK338-dirty", []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2CorrectionStart(context.Background(), good); err == nil {
		t.Fatal("dirty correction lane was accepted")
	}
	if err := os.Remove(started.Runtime.WorktreePath + "/TSK338-dirty"); err != nil {
		t.Fatal(err)
	}
	startedCorrection, err := s.TrainV2CorrectionStart(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	if startedCorrection.ItemPosition != 1 || startedCorrection.Attempt.Number != 1 || startedCorrection.Record.CurrentItemPosition != 1 {
		t.Fatalf("unexpected correction start: %#v", startedCorrection)
	}
	final, err := s.trainV2ReadShared(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Items[0].Review == nil || final.Items[0].Review.Outcome != model.ReviewOutcomeRejectedCorrection || final.Items[1].Status != model.TrainV2ItemRunning || len(final.Items[1].Attempts) != 1 {
		t.Fatalf("correction did not preserve rejected history: %#v", final)
	}
}
