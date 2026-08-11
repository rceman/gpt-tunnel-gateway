package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func enableTrainV2ForTest(t *testing.T, s *Service, hubRevision string) string {
	t.Helper()
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	modelName := "train_v2"
	updated, operation, err := s.ProjectConfigurationUpdate(trustedWorkflowPolicyContext(context.Background(), "planner"), ProjectConfigurationUpdateInput{
		ProjectID:        "example",
		ExpectedRevision: configuration.Revision,
		Patch:            ProjectConfigurationPatch{ExecutionModel: &modelName},
		UpdatedBy:        "planner",
		WriteOptions:     WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ExecutionModel != "train_v2" || operation.Status != "updated" {
		t.Fatalf("unexpected train_v2 configuration: %#v %#v", updated, operation)
	}
	return operation.Hub.After
}

func TestTaskAuthoringCreateUpdateReadyAndRead(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	ctx := context.Background()
	task, operation, err := s.TaskAuthoringCreate(ctx, TaskAuthoringCreateInput{
		ProjectID:             "example",
		Title:                 "Bounded train task",
		Objective:             "Create a branchless planned specification.",
		AcceptanceCriteria:    []string{"planned is durable", "ready seals the exact revision"},
		Constraints:           []string{"no branch or worktree identity"},
		Priority:              "high",
		PreparationReferences: []string{"GTW-ADR11"},
		Metadata:              map[string]string{"lane": "train_v2"},
		ADRRelation:           model.TaskADRNoRequired,
		CreatedBy:             "planner",
		WriteOptions:          WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "EXM-TSK1" || task.Status != model.TaskAuthoringPlanned || task.ReadySeal != nil || operation.Status != "planned" {
		t.Fatalf("unexpected created task: %#v %#v", task, operation)
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"branch", "base_revision", "worktree", "agent_id", "session_id"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("execution identity leaked into Task authoring: %s", encoded)
		}
	}
	read, err := s.TaskAuthoringRead(ctx, "example", task.ID)
	if err != nil || read.RevisionSHA256 != task.RevisionSHA256 {
		t.Fatalf("read mismatch: %#v %v", read, err)
	}

	newTitle := "Updated bounded train task"
	updated, updateOp, err := s.TaskAuthoringUpdate(ctx, TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Title:                  &newTitle,
		UpdatedBy:              "planner",
		WriteOptions:           WriteOptions{ExpectedHubRevision: operation.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Status != model.TaskAuthoringPlanned || updateOp.Status != model.TaskAuthoringPlanned || updated.Title != newTitle {
		t.Fatalf("unexpected update: %#v %#v", updated, updateOp)
	}
	ready, readyOp, err := s.TaskAuthoringReady(ctx, TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       updated.Revision,
		ExpectedRevisionSHA256: updated.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions:           WriteOptions{ExpectedHubRevision: updateOp.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != model.TaskAuthoringReady || ready.ReadySeal == nil || ready.ReadySeal.Revision != ready.Revision || readyOp.Status != "ready" {
		t.Fatalf("unexpected ready task: %#v %#v", ready, readyOp)
	}
	idempotent, idempotentOp, err := s.TaskAuthoringReady(ctx, TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: ready.Revision, ExpectedRevisionSHA256: ready.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: readyOp.Hub.After}})
	if err != nil || idempotent.Revision != ready.Revision || idempotentOp.Status != model.TaskAuthoringReady {
		t.Fatalf("ready idempotency failed: %#v %#v %v", idempotent, idempotentOp, err)
	}

	constraints := []string{"edited after readiness"}
	planned, plannedOp, err := s.TaskAuthoringUpdate(ctx, TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       ready.Revision,
		ExpectedRevisionSHA256: ready.RevisionSHA256,
		Constraints:            &constraints,
		UpdatedBy:              "planner",
		WriteOptions:           WriteOptions{ExpectedHubRevision: readyOp.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.Status != model.TaskAuthoringPlanned || planned.ReadySeal != nil || plannedOp.Status != model.TaskAuthoringPlanned {
		t.Fatalf("ready edit did not invalidate seal: %#v %#v", planned, plannedOp)
	}
}

func TestTaskAuthoringReadyRequiresAcceptedSameProjectADR(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	ctx := context.Background()
	adrResult, err := s.ADRCreate(ctx, ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: "Accepted decision", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences"}, WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := s.TaskAuthoringCreate(ctx, TaskAuthoringCreateInput{ProjectID: "example", Title: "ADR-linked task", Objective: "Require accepted ADR relation.", ADRRelation: model.TaskADRImplementsExisting, ADRReferences: []string{"EXM-ADR1"}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: adrResult.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.TaskAuthoringReady(ctx, TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: operation.Hub.After}})
	if err != nil || ready.Status != model.TaskAuthoringReady {
		t.Fatalf("accepted ADR task was not readied: %#v %v", ready, err)
	}
	bad, badOperation, err := s.TaskAuthoringCreate(ctx, TaskAuthoringCreateInput{ProjectID: "example", Title: "Bad ADR task", Objective: "Reject missing ADR at readiness.", ADRRelation: model.TaskADRImplementsExisting, ADRReferences: []string{"EXM-ADR99"}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: ""}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringReady(ctx, TaskAuthoringReadyInput{ProjectID: "example", TaskID: bad.ID, ExpectedRevision: bad.Revision, ExpectedRevisionSHA256: bad.RevisionSHA256, ReadyBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: badOperation.Hub.After}}); err == nil {
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

func adoptAuthoringIdentifiersForTest(t *testing.T, s *Service, hubRevision string) string {
	t.Helper()
	result, operation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision}})
	if err != nil || result.ProjectCode != "EXM" || result.NextTaskNumber != 1 || operation.Status != "adopted" {
		t.Fatalf("unexpected identifiers: %#v %#v %v", result, operation, err)
	}
	return operation.Hub.After
}
