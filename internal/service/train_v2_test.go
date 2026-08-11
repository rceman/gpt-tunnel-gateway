package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func readyTrainTaskForTest(t *testing.T, s *Service, hubRevision, title string) (model.TaskAuthoring, string) {
	t.Helper()
	task, created, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{ProjectID: "example", Title: title, Objective: "Produce one exact ready Task for Train admission.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	ready, operation, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	return ready, operation.Hub.After
}

func TestTrainV2CreateAddReadListAdmitsExactReadySnapshots(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	first, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "First train item")
	second, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Second train item")
	third, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Third train item")

	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{first.ID, second.ID}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	if train.ID != "EXM-TRN1" || train.Status != model.TrainV2Planned || train.Revision != 1 || len(train.Items) != 2 || operation.Status != model.TrainV2Planned {
		t.Fatalf("unexpected train creation: %#v %#v", train, operation)
	}
	if train.Items[0].TaskID != first.ID || train.Items[0].TaskRevision != first.Revision || train.Items[0].TaskRevisionSHA256 != first.RevisionSHA256 || train.Items[1].TaskID != second.ID {
		t.Fatalf("train did not snapshot ready Tasks: %#v", train.Items)
	}
	encoded, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"branch", "base_revision", "worktree", "agent_id", "session_id"} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("B train contains execution identity %q: %s", forbidden, encoded)
		}
	}

	added, addOperation, err := s.TrainV2Add(context.Background(), TrainV2AddInput{ProjectID: "example", TrainID: train.ID, TaskIDs: []string{third.ID}, ExpectedRevision: train.Revision, AddedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if added.Revision != 2 || len(added.Items) != 3 || added.Items[2].Position != 2 || added.Items[2].TaskID != third.ID || addOperation.Status != model.TrainV2Planned {
		t.Fatalf("unexpected train append: %#v %#v", added, addOperation)
	}
	if _, _, err := s.TrainV2Add(context.Background(), TrainV2AddInput{ProjectID: "example", TrainID: train.ID, TaskIDs: []string{first.ID}, ExpectedRevision: added.Revision, AddedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: addOperation.Hub.After}}); err == nil {
		t.Fatal("duplicate Task admission was accepted")
	}
	planned, plannedOperation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{ProjectID: "example", Title: "Not ready", Objective: "Must not enter a Train before ready.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{planned.ID}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: plannedOperation.Hub.After}}); err == nil {
		t.Fatal("planned Task was admitted to a Train")
	}

	read, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil || read.Revision != added.Revision || len(read.Items) != 3 {
		t.Fatalf("train read mismatch: %#v %v", read, err)
	}
	listed, err := s.TrainV2List(context.Background(), TrainV2ListInput{ProjectID: "example", Limit: 10})
	if err != nil || len(listed.Trains) != 1 || listed.Trains[0].ID != train.ID {
		t.Fatalf("train list mismatch: %#v %v", listed, err)
	}
}

func TestTrainV2RejectsCrossProjectAndLegacyModeAdmission(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{"EXM-TSK1"}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}}); err == nil {
		t.Fatal("legacy project accepted Train v2 creation")
	}
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{"ZZZ-TSK1"}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}}); err == nil {
		t.Fatal("cross-project Task was accepted")
	}
}
