package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2StartBindsExactItemLocalAttempt(t *testing.T) {
	s, hubRevision, _ := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Attempt start")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Attempt.Number != 1 || started.Attempt.AgentID != "coder-example" || started.Attempt.AirelaySessionKey != "example_master" || started.Record.CurrentItemPosition != 0 || started.Record.CurrentAttemptNumber != 1 {
		t.Fatalf("unexpected Attempt start: %#v", started)
	}
	if _, err := os.Stat(filepath.Join(s.Config.StateDir, "runs")); !os.IsNotExist(err) {
		t.Fatalf("Train-v2 start created legacy runs storage: %v", err)
	}
}

func TestTrainV2StartRejectsAttemptSessionMismatch(t *testing.T) {
	s, hubRevision, _ := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Attempt session")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		AgentID:   "unknown",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err == nil {
		t.Fatal("unknown Attempt owner was accepted")
	}
}

func TestTrainV2AttemptValidationRemainsStrict(t *testing.T) {
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "agent-one", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", StartedAt: nowUTC()}
	if err := model.ValidateTrainV2Attempt(attempt); err != nil {
		t.Fatal(err)
	}
}
