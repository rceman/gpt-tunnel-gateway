package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestWatcherNudgeUsesExactTrainAttemptSession(t *testing.T) {
	s, revision, _ := testService(t)
	config := s.Config.Projects["example"]
	config.Watcher.NudgeEnabled = true
	s.Config.Projects["example"] = config
	revision = enableTrainV2ForTest(t, s, revision)
	task, revision := readyTrainTaskForTest(t, s, revision, "watcher Attempt")
	train, op, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	seedTrainExecutionSession(t, s, train.ID)
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: op.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.WatcherNudge(context.Background(), WatcherNudgeInput{
		ProjectID: "example",
		Text:      "continue from checkpoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Delivered || receipt.TrainID != train.ID || receipt.TaskID != task.ID || receipt.ItemPosition != started.ItemPosition || receipt.AttemptNumber != started.Attempt.Number {
		t.Fatalf("unexpected watcher nudge receipt: %#v", receipt)
	}
	if err := model.ValidateWatcherNudgeReceipt(receipt); err != nil {
		t.Fatal(err)
	}
}
