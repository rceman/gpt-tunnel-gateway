package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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

func TestTrainV2ListSortsGloballyBeforeApplyingLimit(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	first, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "First list ordering item")
	second, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Second list ordering item")
	_, firstOperation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example", TaskIDs: []string{first.ID}, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondTrain, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example", TaskIDs: []string{second.ID}, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: firstOperation.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := s.TrainV2List(context.Background(), TrainV2ListInput{ProjectID: "example", Limit: 1})
	if err != nil || len(listed.Trains) != 1 || listed.Trains[0].ID != secondTrain.ID {
		t.Fatalf("global newest-first ordering lost: trains=%#v err=%v", listed.Trains, err)
	}
}

func TestTrainV2ServiceKeepsAuthorityAndProjectGuards(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{"EXM-TSK1"},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err == nil {
		t.Fatal("legacy project accepted Train v2 creation")
	}
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{"ZZZ-TSK1"},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err == nil {
		t.Fatal("cross-project Task was accepted")
	}
}

func TestTrainV2CreateIgnoresIntegrationAuxiliaryRecord(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	first, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Train admission with auxiliary state")
	operation, err := s.Hub.Transact(context.Background(), hubRevision, "test: add Train integration auxiliary record", func(worktree string) ([]string, error) {
		path := s.trainV2Root("example") + "/EXM-TRN4.integration-operation.json"
		if err := hub.WriteJSON(worktree, path, map[string]any{"operation_id": "integration-1"}); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.After,
		},
	})
	if err != nil {
		t.Fatalf("Train/create treated integration auxiliary JSON as a Train: %v", err)
	}
	if created.ID != "EXM-TRN1" || len(created.Items) != 1 {
		t.Fatalf("unexpected Train created with auxiliary record present: %#v", created)
	}
}

func TestTrainV2AllScannersIgnoreIntegrationAuxiliaryRecords(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Auxiliary scanner coverage")
	auxiliaryPath := s.trainV2Root("example") + "/EXM-TRN7.integration-operation.json"
	operation, err := s.Hub.Transact(context.Background(), hubRevision, "test: add Train integration auxiliary record", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, auxiliaryPath, map[string]any{"operation_id": "integration-7"}); err != nil {
			return nil, err
		}
		if err := activeTrainInHubWorktree(worktree, "example"); err != nil {
			return nil, err
		}
		return []string{auxiliaryPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := s.TrainV2List(context.Background(), TrainV2ListInput{
		ProjectID: "example",
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil || len(listed.Trains) != 0 {
		t.Fatalf("auxiliary record was treated as Train: trains=%#v err=%v", listed.Trains, err)
	}
	active, err := s.projectHasActiveTrainAttempt(context.Background(), "example")
	if err != nil || active {
		t.Fatalf("auxiliary record affected active-attempt scan: active=%v err=%v", active, err)
	}
	if err := s.rejectActiveTrains(context.Background(), "example"); err != nil {
		t.Fatalf("auxiliary record affected project removal scan: %v", err)
	}
	worktree := t.TempDir()
	root := filepath.Join(worktree, filepath.FromSlash(hubTrainRoot("example")))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "EXM-TRN8.integration.json"), []byte(`{"operation_id":"integration-8"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	active, err = activeTrainAttemptInWorktree(worktree, "example")
	if err != nil || active {
		t.Fatalf("worktree auxiliary record affected active-attempt scan: active=%v err=%v", active, err)
	}
	title := "Auxiliary scanner coverage updated"
	updated, _, err := s.TaskAuthoringUpdate(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Title:                  &title,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.After,
		},
	})
	if err != nil || updated.Title != "Auxiliary scanner coverage updated" {
		t.Fatalf("task/update decoded auxiliary record as Train: task=%#v err=%v", updated, err)
	}
}

func TestTrainV2RecordFilterStillRejectsMalformedCanonicalRecord(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	path := s.trainV2Root("example") + "/EXM-TRN9.json"
	if _, err := s.Hub.Transact(context.Background(), hubRevision, "test: add malformed canonical Train", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, map[string]any{"operation_id": "not-a-train"}); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2List(context.Background(), TrainV2ListInput{
		ProjectID: "example",
		Limit:     model.MaxTrainV2Items,
	}); err == nil {
		t.Fatal("malformed canonical Train record was silently skipped")
	}
}
