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
	task, created, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       title,
		Objective:   "Produce one exact ready Task for Train admission.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, operation, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ready, operation.Hub.After
}

// This is intentionally a persistence/wiring smoke test. Train transition
// behavior is covered without Hub/Git setup in internal/train.
func TestTrainV2ServicePersistsPureAdmissionResults(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	first, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "First train item")
	second, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Second train item")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if train.ID != "EXM-TRN1" || train.Status != model.TrainV2Planned || len(train.Items) != 1 || operation.Status != model.TrainV2Planned {
		t.Fatalf("unexpected persisted Train: %#v %#v", train, operation)
	}
	boundFirst, err := s.TaskAuthoringRead(context.Background(), "example", first.ID)
	if err != nil || boundFirst.Execution != model.TaskExecutionTrain || train.Items[0].TaskRevision != boundFirst.Revision || train.Items[0].TaskRevisionSHA256 != boundFirst.RevisionSHA256 {
		t.Fatalf("Train create did not atomically bind first Task: task=%#v train=%#v err=%v", boundFirst, train, err)
	}
	added, addOperation, err := s.TrainV2Add(context.Background(), TrainV2AddInput{
		ProjectID:        "example",
		TrainID:          train.ID,
		TaskIDs:          []string{second.ID},
		ExpectedRevision: train.Revision,
		AddedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.Revision != 2 || len(added.Items) != 2 || addOperation.Status != model.TrainV2Planned {
		t.Fatalf("unexpected persisted append: %#v %#v", added, addOperation)
	}
	boundSecond, err := s.TaskAuthoringRead(context.Background(), "example", second.ID)
	if err != nil || boundSecond.Execution != model.TaskExecutionTrain || added.Items[1].TaskRevision != boundSecond.Revision || added.Items[1].TaskRevisionSHA256 != boundSecond.RevisionSHA256 {
		t.Fatalf("Train add did not atomically bind second Task: task=%#v train=%#v err=%v", boundSecond, added, err)
	}
	read, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil || read.Revision != added.Revision {
		t.Fatalf("read wiring failed: %#v %v", read, err)
	}
	listed, err := s.TrainV2List(context.Background(), TrainV2ListInput{
		ProjectID: "example",
		Limit:     10,
	})
	if err != nil || len(listed.Trains) != 1 {
		t.Fatalf("list wiring failed: %#v %v", listed, err)
	}
	encoded, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"branch", "base_revision", "worktree", "agent_id", "session_id"} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("execution identity leaked into portable Train: %s", encoded)
		}
	}
}
func TestTrainV2CreateRejectsTaskAlreadyAdmittedByAnotherTrain(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Duplicate Train membership")
	_, firstOperation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
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
	before := firstOperation.Hub.After
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: before,
		},
	}); err == nil || !strings.Contains(err.Error(), "already belongs to train") {
		t.Fatalf("duplicate Task membership was not rejected without mutation: %v", err)
	}
	after, err := s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("duplicate membership changed Hub revision from %s to %s", before, after)
	}
}
func TestTrainV2ListSortsGloballyBeforeApplyingLimit(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	first, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "First list ordering item")
	second, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Second list ordering item")
	_, firstOperation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondTrain, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{second.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: firstOperation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := s.TrainV2List(context.Background(), TrainV2ListInput{
		ProjectID: "example",
		Limit:     1,
	})
	if err != nil || len(listed.Trains) != 1 || listed.Trains[0].ID != secondTrain.ID {
		t.Fatalf("global newest-first ordering lost: trains=%#v err=%v", listed.Trains, err)
	}
}
