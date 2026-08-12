package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2StartUsesSingleCodingAgentAutoBinding(t *testing.T) {
	s, hubRevision, _ := testService(t)
	delete(s.Config.AgentBindings, config.ProjectAgentBindingKey("example", "coder-example"))
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Auto-bound Train start")
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
	if started.Run.AgentID != "coder-example" || started.Run.SessionKey != "example_master" || started.Record.Status != model.TrainV2StartActive {
		t.Fatalf("Train/start did not preserve the auto-bound host identity: %#v", started)
	}
}
