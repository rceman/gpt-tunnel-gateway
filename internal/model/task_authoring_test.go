package model

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func validTaskAuthoringForTest() TaskAuthoring {
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	task := TaskAuthoring{
		SchemaVersion:         TaskAuthoringSchemaVersion,
		ID:                    "EXM-TSK1",
		ProjectID:             "example",
		Revision:              1,
		Title:                 "Bounded task authoring",
		Objective:             "Validate the planned and ready task contract.",
		AcceptanceCriteria:    []string{"planned state is durable", "ready seal is exact"},
		Constraints:           []string{"no execution identity in the Task"},
		Priority:              "high",
		PreparationReferences: []string{"GTW-ADR11"},
		Metadata:              map[string]string{"mode": "train_v2"},
		ADRRelation:           TaskADRImplementsExisting,
		ADRReferences:         []string{"GTW-ADR11"},
		Status:                TaskAuthoringPlanned,
		CreatedBy:             "planner",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	digest, err := HashTaskAuthoring(task)
	if err != nil {
		panic(err)
	}
	task.RevisionSHA256 = digest
	return task
}

func TestTaskAuthoringValidationAndReadySeal(t *testing.T) {
	task := validTaskAuthoringForTest()
	if err := ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	if task.ReadySeal != nil {
		t.Fatal("planned task unexpectedly has ready seal")
	}
	task.Status = TaskAuthoringReady
	task.ReadySeal = &TaskReadySeal{
		Revision:       task.Revision,
		RevisionSHA256: task.RevisionSHA256,
		ReadyBy:        "planner",
		ReadyAt:        time.Date(2026, 8, 12, 10, 1, 0, 0, time.UTC),
	}
	if err := ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	task.ReadySeal.Revision++
	if err := ValidateTaskAuthoring(task); err == nil {
		t.Fatal("mismatched ready seal was accepted")
	}
}

func TestTaskAuthoringValidationRejectsExecutionIdentity(t *testing.T) {
	task := validTaskAuthoringForTest()
	if err := ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	// Execution identity is intentionally not representable in TaskAuthoring;
	// the strict Hub decoder rejects such fields before validation.
	var decoded TaskAuthoring
	decoder := json.NewDecoder(bytes.NewReader([]byte(`{"id":"EXM-TSK1","branch":"feature/not-a-task-field"}`)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err == nil {
		t.Fatal("execution identity field was accepted by strict decoder")
	}
}
