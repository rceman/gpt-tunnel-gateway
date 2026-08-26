package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	ready, _, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
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
	if s.Durability == nil {
		t.Fatal("Shared authority is unavailable")
	}
	entries, err := s.Durability.PendingOutbox(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := s.publishSharedOutboxEntry(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
		if err := s.Durability.MarkOutboxPublished(context.Background(), entry.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ready, revision
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
	before, err := s.TrainV2List(context.Background(), TrainV2ListInput{ProjectID: "example"})
	if err != nil || len(before.Trains) != 1 {
		t.Fatalf("unexpected Shared Train baseline: %#v err=%v", before, err)
	}
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: firstOperation.Hub.After,
		},
	}); err == nil || !strings.Contains(err.Error(), "already belongs to train") {
		t.Fatalf("duplicate Task membership was not rejected without mutation: %v", err)
	}
	after, err := s.TrainV2List(context.Background(), TrainV2ListInput{ProjectID: "example"})
	if err != nil || len(after.Trains) != 1 || after.Trains[0].Revision != before.Trains[0].Revision {
		t.Fatalf("duplicate membership changed Shared authority: before=%#v after=%#v err=%v", before, after, err)
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
