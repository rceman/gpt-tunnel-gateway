package train

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAdmissionBuildsOnlyExactReadySnapshots(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "GTW-TSK179", ProjectID: "gateway", Revision: 1, Title: "Ready task", Objective: "A ready task for admission.", ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringReady, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, ReadySeal: &model.TaskReadySeal{Revision: 1, ReadyBy: "planner", ReadyAt: now}}
	digest, err := model.HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	task.RevisionSHA256 = digest
	task.ReadySeal.RevisionSHA256 = digest
	items, err := ReadyItems([]model.TaskAuthoring{task}, now, 0)
	if err != nil || len(items) != 1 || items[0].TaskRevisionSHA256 != digest {
		t.Fatalf("unexpected admission: %#v %v", items, err)
	}
	task.Status = model.TaskAuthoringPlanned
	if _, err := ReadyItems([]model.TaskAuthoring{task}, now, 0); err == nil {
		t.Fatal("planned task was admitted")
	}
}

func readyAdmissionTask(t *testing.T, id string, now time.Time) model.TaskAuthoring {
	t.Helper()
	draft := AuthoringDraft{
		Title:       "Ready " + id,
		Objective:   "A ready task for train admission.",
		ADRRelation: model.TaskADRNoRequired,
	}
	task, err := NewTask("gateway", id, draft, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := ReadyTask(task, "planner", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return ready
}

func TestAdmissionCreatesAndAppendsWithInMemoryTasks(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	first := readyAdmissionTask(t, "GTW-TSK181", now)
	second := readyAdmissionTask(t, "GTW-TSK182", now)
	train, err := New("gateway", "GTW-TRN1", "planner", []model.TaskAuthoring{first}, now)
	if err != nil {
		t.Fatal(err)
	}
	if train.Status != model.TrainV2Planned || len(train.Items) != 1 || train.Items[0].TaskRevisionSHA256 != first.RevisionSHA256 {
		t.Fatalf("unexpected train snapshot: %#v", train)
	}
	if err := ValidateUnadmitted([]model.TrainV2{train}, []string{first.ID}); err == nil {
		t.Fatal("duplicate task admission was accepted")
	}
	updated, err := Append(train, []model.TaskAuthoring{second}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || len(updated.Items) != 2 || updated.Items[1].Position != 1 || updated.Items[1].TaskID != second.ID {
		t.Fatalf("unexpected appended train: %#v", updated)
	}
	planned := second
	planned.Status = model.TaskAuthoringPlanned
	planned.ReadySeal = nil
	planned.RevisionSHA256, err = model.HashTaskAuthoring(planned)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Append(updated, []model.TaskAuthoring{planned}, now); err == nil {
		t.Fatal("planned task was admitted")
	}
}

func TestAdmissionRejectsInvalidExistingTrainBeforeCheckingCandidates(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	task := readyAdmissionTask(t, "GTW-TSK183", now)
	invalid := model.TrainV2{ID: "GTW-TRN1", ProjectID: "gateway", Status: model.TrainV2Planned}
	if err := ValidateUnadmitted([]model.TrainV2{invalid}, []string{task.ID}); err == nil {
		t.Fatal("invalid existing train was ignored")
	}
}
