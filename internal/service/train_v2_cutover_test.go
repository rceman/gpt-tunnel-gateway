package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTrainV2RetiresPlanAndLegacySchedulerAndUsesPlanFreePacket(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	if _, err := s.PlanUpdate(context.Background(), PlanUpdateInput{
		ProjectID: "example",
		UpdatedBy: "planner",
	}); err == nil || !strings.Contains(err.Error(), "PLAN_AUTHORITY_RETIRED") {
		t.Fatalf("Plan mutation was not retired: %v", err)
	}
	if _, _, err := s.TaskTrainCreate(context.Background(), TaskTrainCreateInput{
		ProjectID: "example",
		TaskIDs:   []string{"EXM-TSK1"},
		CreatedBy: "planner",
	}); err == nil || !strings.Contains(err.Error(), "TRAIN_V2_AUTHORITY") {
		t.Fatalf("legacy scheduler was not retired: %v", err)
	}
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Packet task")
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
	packet, err := s.TrainV2TaskRead(context.Background(), "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Train.ID != train.ID || packet.Item.TaskID != task.ID || packet.ProjectConfiguration.ExecutionModel != "train_v2" || packet.WorkflowPolicy.ProjectID != "example" || operation.Status != model.TrainV2Planned {
		t.Fatalf("unexpected Train packet: %#v", packet)
	}
	encoded, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"plan"`) || strings.Contains(packet.Text, "global plan") {
		t.Fatalf("Train packet retained Plan authority: %s", encoded)
	}
}
