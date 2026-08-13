package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2AdvanceStartsNextItemAndIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
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

	hubRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	completed, err := s.Hub.Transact(context.Background(), hubRevision, "test: complete first Train item", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path("example", train.ID), &latest); err != nil {
			return nil, err
		}
		attempt := &latest.Items[0].Attempts[0]
		attempt.Status = model.TrainV2AttemptSucceeded
		attempt.FinishedAt = &finished
		latest.Items[0].Status = model.TrainV2ItemFinalized
		latest.Items[0].ActiveAttemptNumber = 0
		latest.Items[0].SuccessfulAttemptNumber = 1
		latest.Revision++
		latest.UpdatedAt = finished
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, latest); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	advanced, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   train.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: completed.After,
		},
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
	if len(updated.Items[0].Attempts) != 1 || updated.Items[0].Status != model.TrainV2ItemFinalized || len(updated.Items[1].Attempts) != 1 || updated.Items[1].Status != model.TrainV2ItemRunning {
		t.Fatalf("unexpected persisted next-item state: %#v", updated)
	}

	beforeRetry, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retried, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   train.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: beforeRetry,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterRetry, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if afterRetry != beforeRetry || retried.Attempt.Number != 1 || retried.ItemPosition != 1 {
		t.Fatalf("advance retry was not idempotent: before=%s after=%s result=%#v", beforeRetry, afterRetry, retried)
	}
}
