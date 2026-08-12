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
	return tx.After
}

// The service package checks Hub/path/authority wiring only. Semantic task
// transitions are exercised with in-memory values in internal/train.
func TestTaskAuthoringServiceWiresCanonicalLifecycle(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	ctx := context.Background()
	task, operation, err := s.TaskAuthoringCreate(ctx, TaskAuthoringCreateInput{ProjectID: "example", Title: "Bounded train task", Objective: "Create a branchless planned specification.", AcceptanceCriteria: []string{"planned is durable"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil || task.Status != model.TaskAuthoringPlanned || operation.Status != model.TaskAuthoringPlanned {
		t.Fatalf("create wiring failed: %#v %#v %v", task, operation, err)
	}
	read, err := s.TaskAuthoringRead(ctx, "example", task.ID)
	if err != nil || read.RevisionSHA256 != task.RevisionSHA256 {
		t.Fatalf("read wiring failed: %#v %v", read, err)
	}
	newTitle := "Updated bounded train task"
	updated, updateOperation, err := s.TaskAuthoringUpdate(ctx, TaskAuthoringUpdateInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, Title: &newTitle, UpdatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil || updated.Revision != 2 || updateOperation.Status != model.TaskAuthoringPlanned {
		t.Fatalf("update wiring failed: %#v %#v %v", updated, updateOperation, err)
	}
	ready, readyOperation, err := s.TaskAuthoringReady(ctx, TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: updated.Revision, ExpectedRevisionSHA256: updated.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: updateOperation.Hub.After}})
	if err != nil || ready.Status != model.TaskAuthoringReady || ready.ReadySeal == nil || readyOperation.Status != model.TaskAuthoringReady {
		t.Fatalf("ready wiring failed: %#v %#v %v", ready, readyOperation, err)
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

func TestTaskAuthoringServiceWiresADRReadiness(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	adrResult, err := s.ADRCreate(context.Background(), ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: "Accepted decision", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences"}, WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{ProjectID: "example", Title: "ADR-linked task", Objective: "Require accepted ADR relation.", ADRRelation: model.TaskADRImplementsExisting, ADRReferences: []string{"EXM-ADR1"}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: adrResult.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil || ready.Status != model.TaskAuthoringReady {
		t.Fatalf("accepted ADR task was not readied: %#v %v", ready, err)
	}
	bad, badOperation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{ProjectID: "example", Title: "Bad ADR task", Objective: "Reject missing ADR at readiness.", ADRRelation: model.TaskADRImplementsExisting, ADRReferences: []string{"EXM-ADR99"}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{ProjectID: "example", TaskID: bad.ID, ExpectedRevision: bad.Revision, ExpectedRevisionSHA256: bad.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: badOperation.Hub.After}}); err == nil {
		t.Fatal("missing ADR task was readied")
	}
}

func TestTaskAuthoringRequiresTrainV2AndOptimisticRevision(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	if _, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{ProjectID: "example", Title: "Legacy blocked", Objective: "Must require train_v2.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}}); err == nil {
		t.Fatal("legacy project accepted train_v2 authoring")
	}
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{ProjectID: "example", Title: "Revision guard", Objective: "Exercise optimistic revision.", ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringUpdate(context.Background(), TaskAuthoringUpdateInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision + 1, ExpectedRevisionSHA256: task.RevisionSHA256, UpdatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}}); err == nil {
		t.Fatal("stale revision was accepted")
	}
}

func jsonFieldPresent(data []byte, field string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	_, ok := fields[field]
	return ok
}

func adoptAuthoringIdentifiersForTest(t *testing.T, s *Service, hubRevision string) string {
	t.Helper()
	result, operation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil || result.ProjectCode != "EXM" || result.NextTaskNumber != 1 || operation.Status != "adopted" {
		t.Fatalf("unexpected identifiers: %#v %#v %v", result, operation, err)
	}
	return operation.Hub.After
}
