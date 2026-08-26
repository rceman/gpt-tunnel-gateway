package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
)

func TestTrainV2AdvanceStartsNextItemAndIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	revision = enableTrainV2ForTest(t, s, revision)
	first, revision := readyTrainTaskForTest(t, s, revision, "first item")
	second, revision := readyTrainTaskForTest(t, s, revision, "second item")
	train, created, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID},
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
		TaskIDs:          []string{second.ID},
		ExpectedRevision: train.Revision,
		AddedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ensureTaskWorkMirrorForTest(t, s)
	_, err = s.TrainV2Start(context.Background(), TrainV2StartInput{
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
	finished := time.Now().UTC()
	latest, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	{
		attempt := &latest.Items[0].Attempts[0]
		attempt.Status = model.TrainV2AttemptSucceeded
		attempt.FinishedAt = &finished
		attempt.ReportID = "implementation-report"
		attempt.ReviewID = "review-report"
		latest.Items[0].Status = model.TrainV2ItemReviewed
		latest.Items[0].ActiveAttemptNumber = 0
		latest.Items[0].SuccessfulAttemptNumber = 1
		latest.Items[0].Proof = &model.TrainV2ImplementationProof{
			CheckpointHead:    strings.Repeat("a", 40),
			ImplementationSHA: strings.Repeat("b", 40),
			ReportID:          attempt.ReportID,
			GateResults:       []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}},
			RecordedAt:        finished,
		}
		latest.Items[0].Review = &model.TrainV2ItemReview{
			Outcome:    model.ReviewOutcomeAccepted,
			ReportID:   attempt.ReviewID,
			ReviewedAt: finished,
		}
		latest.Revision++
		latest.UpdatedAt = finished
		if err := model.ValidateTrainV2(latest); err != nil {
			t.Fatal(err)
		}
		if err := s.commitSharedTrain(context.Background(), "test-complete-"+train.ID, latest, "test-complete"); err != nil {
			t.Fatal(err)
		}
	}

	advanced, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   train.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if advanced.ItemPosition != 1 || advanced.Attempt.Number != 1 || advanced.Attempt.DispatchedAt == nil || advanced.Record.CurrentItemPosition != 1 {
		t.Fatalf("unexpected next-item progression: %#v", advanced)
	}
	updated, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items[0].Attempts) != 1 || updated.Items[0].Status != model.TrainV2ItemReviewed || len(updated.Items[1].Attempts) != 1 || updated.Items[1].Status != model.TrainV2ItemRunning {
		t.Fatalf("unexpected persisted next-item state: %#v", updated)
	}

	retried, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   train.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if retried.Attempt.Number != 1 || retried.ItemPosition != 1 {
		t.Fatalf("advance retry was not idempotent: result=%#v", retried)
	}
}
