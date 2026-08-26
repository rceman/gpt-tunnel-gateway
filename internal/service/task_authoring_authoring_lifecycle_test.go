package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func enableTrainV2ForTest(t *testing.T, s *Service, hubRevision string) string {
	t.Helper()
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	expected := hubRevision
	tx, err := s.Hub.Transact(context.Background(), expected, "test: seed train_v2 authority", func(worktree string) ([]string, error) {
		var latest model.ProjectConfiguration
		if err := readWorktreeJSON(worktree, s.projectConfigurationPath("example"), &latest); err != nil {
			return nil, err
		}
		latest.ExecutionModel = "train_v2"
		latest.Revision = configuration.Revision + 1
		if err := model.ValidateProjectConfiguration(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.projectConfigurationPath("example"), latest); err != nil {
			return nil, err
		}
		return []string{s.projectConfigurationPath("example")}, nil
	})
	if err != nil {
		t.Fatalf("seed train_v2 configuration: %v", err)
	}
	if err := s.BootstrapSharedFromHub(context.Background()); err != nil {
		t.Fatal(err)
	}
	return tx.After
}

// The service package checks Hub/path/authority wiring only. Semantic task
// transitions are exercised with in-memory values in internal/train.
func TestTaskAuthoringServiceWiresCanonicalLifecycle(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	ctx := context.Background()
	task, operation, err := s.TaskAuthoringCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Bounded train task",
		Objective:          "Create a branchless planned specification.",
		AcceptanceCriteria: []string{"planned is durable"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil || task.Status != model.TaskAuthoringPlanned || operation.Status != model.TaskAuthoringPlanned {
		t.Fatalf("create wiring failed: %#v %#v %v", task, operation, err)
	}
	if err := s.BootstrapSharedFromHub(ctx); err != nil {
		t.Fatal(err)
	}
	read, err := s.TaskAuthoringRead(ctx, "example", task.ID)
	if err != nil || read.RevisionSHA256 != task.RevisionSHA256 {
		t.Fatalf("read wiring failed: %#v %v", read, err)
	}
	newTitle := "Updated bounded train task"
	updated, updateOperation, err := s.TaskAuthoringUpdate(ctx, TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Title:                  &newTitle,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil || updated.Revision != 2 || updateOperation.Status != model.TaskAuthoringPlanned {
		t.Fatalf("update wiring failed: %#v %#v %v", updated, updateOperation, err)
	}
	if err := s.BootstrapSharedFromHub(ctx); err != nil {
		t.Fatal(err)
	}
	ready, readyOperation, err := s.TaskAuthoringReady(ctx, TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       updated.Revision,
		ExpectedRevisionSHA256: updated.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: updateOperation.Hub.After,
		},
	})
	if err != nil || ready.Status != model.TaskAuthoringReady || ready.ReadySeal == nil || readyOperation.Status != model.TaskAuthoringReady {
		t.Fatalf("ready wiring failed: %#v %#v %v", ready, readyOperation, err)
	}
	if err := s.BootstrapSharedFromHub(ctx); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ready)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"branch", "base_revision", "worktree", "agent_id", "session_id"} {
		if jsonFieldPresent(encoded, forbidden) {
			t.Fatalf("execution identity leaked into authoring record: %s", encoded)
		}
	}
}
func TestTaskAuthoringQueuedTrainItemCanBeUpdatedUntilAttemptStarts(t *testing.T) {
	s, hubRevision, _ := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Queued editable task")
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
	seedSharedTrainForTaskWorkTest(t, s, train)
	seedLocalCodingAgentSessionForTaskWorkTest(t, s)
	ensureTaskWorkMirrorForTest(t, s)
	newTitle := "Queued task edited before execution"
	updated, updateOperation, err := s.TaskAuthoringUpdate(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Title:                  &newTitle,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil || updated.Status != model.TaskAuthoringPlanned || updateOperation.Status != model.TaskAuthoringPlanned {
		t.Fatalf("queued Task update was rejected: task=%#v operation=%#v err=%v", updated, updateOperation, err)
	}

	read, err := s.TaskAuthoringRead(context.Background(), "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID}); err == nil {
		t.Fatal("edited queued Task started without a fresh READY seal")
	}
	planned, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Status != model.TrainV2Planned || len(planned.Items[0].Attempts) != 0 {
		t.Fatalf("unready Task changed Train state: %#v", planned)
	}
	ready, readyOperation, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       read.Revision,
		ExpectedRevisionSHA256: read.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: updateOperation.Hub.After,
		},
	})
	if err != nil || ready.Status != model.TaskAuthoringReady {
		t.Fatalf("edited queued Task was not re-readied: task=%#v err=%v", ready, err)
	}
	started, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID, AgentID: "coder-example"})
	if err != nil || started.AttemptNumber != 1 {
		t.Fatalf("re-readied queued Task did not start: result=%#v err=%v", started, err)
	}
	if _, _, err := s.TaskAuthoringUpdate(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       ready.Revision,
		ExpectedRevisionSHA256: ready.RevisionSHA256,
		Title:                  &newTitle,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: readyOperation.Hub.After,
		},
	}); err == nil {
		t.Fatal("Task update was accepted after Attempt creation")
	}
	if train.ID == "" {
		t.Fatal("Train was not persisted")
	}
}
func TestTaskAuthoringServiceWiresADRReadiness(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	adrResult, err := s.ADRCreate(context.Background(), ADRCreateInput{
		ADR: model.ADR{ProjectID: "example", Title: "Accepted decision", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:     "example",
		Title:         "ADR-linked task",
		Objective:     "Require accepted ADR relation.",
		ADRRelation:   model.TaskADRImplementsExisting,
		ADRReferences: []string{"EXM-ADR1"},
		CreatedBy:     "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: adrResult.Hub.After,
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
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil || ready.Status != model.TaskAuthoringReady {
		t.Fatalf("accepted ADR task was not readied: %#v %v", ready, err)
	}
	bad, badOperation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:     "example",
		Title:         "Bad ADR task",
		Objective:     "Reject missing ADR at readiness.",
		ADRRelation:   model.TaskADRImplementsExisting,
		ADRReferences: []string{"EXM-ADR99"},
		CreatedBy:     "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 bad.ID,
		ExpectedRevision:       bad.Revision,
		ExpectedRevisionSHA256: bad.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: badOperation.Hub.After,
		},
	}); err == nil {
		t.Fatal("missing ADR task was readied")
	}
}
func TestTaskAuthoringRequiresTrainV2AndOptimisticRevision(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	if _, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Legacy blocked",
		Objective:   "Must require train_v2.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err == nil {
		t.Fatal("legacy project accepted train_v2 authoring")
	}
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Revision guard",
		Objective:   "Exercise optimistic revision.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringUpdate(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision + 1,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err == nil {
		t.Fatal("stale revision was accepted")
	}
}
