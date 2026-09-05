package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	boundCorrectionTask, err := s.TaskAuthoringRead(context.Background(), "example", correctionTask.ID)
	if err != nil || boundCorrectionTask.Execution != model.TaskExecutionTrain {
		t.Fatalf("Train admission did not bind correction Task execution: %#v %v", boundCorrectionTask, err)
	}
	seedTrainExecutionSession(t, s, train.ID)
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: added.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewID := fmt.Sprintf("%s-ITEM0-ATTEMPT1-REVIEW", train.ID)
	now := nowUTC()
	seedRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Hub.Transact(context.Background(), seedRevision, "test: seed rejected Train review", func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path("example", train.ID), &current); err != nil {
			return nil, err
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
		if err := model.ValidateTrainV2(current); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path("example", train.ID), current); err != nil {
			return nil, err
		}
		path := trainV2AttemptReviewPath("example", train.ID, 0, 1)
		if err := hub.WriteJSON(worktree, path, review); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path("example", train.ID), path}, nil
	})
	if err != nil {
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
		CorrectionTaskRevision:       boundCorrectionTask.Revision,
		CorrectionTaskRevisionSHA256: boundCorrectionTask.RevisionSHA256,
		StartedBy:                    "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: updated.After,
		},
	}
	if _, err := s.TrainV2CorrectionStart(context.Background(), bad); err == nil {
		t.Fatal("mismatched rejected review was accepted")
	}
	good := bad
	good.RejectedReviewID = reviewID
	if _, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   train.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: updated.After,
		},
	}); err == nil {
		t.Fatal("ordinary advance opened rejected correction")
	}
	wrongTask := bad
	wrongTask.RejectedReviewID, wrongTask.CorrectionTaskID = reviewID, "EXM-TSK999"
	if _, err := s.TrainV2CorrectionStart(context.Background(), wrongTask); err == nil {
		t.Fatal("wrong queued Task identity was accepted")
	}
	wrongRevision := bad
	wrongRevision.RejectedReviewID, wrongRevision.CorrectionTaskRevision = reviewID, boundCorrectionTask.Revision+1
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
	final, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Items[0].Review == nil || final.Items[0].Review.Outcome != model.ReviewOutcomeRejectedCorrection || final.Items[1].Status != model.TrainV2ItemRunning || len(final.Items[1].Attempts) != 1 {
		t.Fatalf("correction did not preserve rejected history: %#v", final)
	}
}
