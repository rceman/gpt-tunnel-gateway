package train

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type taskMemory struct {
	task model.TaskAuthoring
}

func (m *taskMemory) replace(task model.TaskAuthoring) {
	m.task = task
}

func authoringDraft() AuthoringDraft {
	return AuthoringDraft{
		Title:              "Bounded train task",
		Objective:          "Exercise the storage-independent task lifecycle.",
		AcceptanceCriteria: []string{"revision is hashed", "ready is sealed"},
		Constraints:        []string{"no host execution identity"},
		Priority:           "high",
		Metadata:           map[string]string{"lane": "train_v2"},
		ADRRelation:        model.TaskADRNoRequired,
	}
}

func TestAuthoringLifecycleUsesPureStateTransitions(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("gateway", "GTW-TSK179", authoringDraft(), "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskAuthoringPlanned || task.ReadySeal != nil || task.Revision != 1 || task.Type != model.TaskTypeTask || task.Execution != "" {
		t.Fatalf("unexpected planned task: %#v", task)
	}
	memory := &taskMemory{task: task}
	title := "Updated bounded train task"
	updated, changed, err := UpdateTask(memory.task, AuthoringPatch{Title: &title}, "planner", now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("update failed: %#v %v %v", updated, changed, err)
	}
	if updated.Revision != 2 || updated.Status != model.TaskAuthoringPlanned || updated.ReadySeal != nil || updated.Title != title {
		t.Fatalf("unexpected updated task: %#v", updated)
	}
	memory.replace(updated)
	ready, err := ReadyTask(memory.task, "planner", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != model.TaskAuthoringReady || ready.ReadySeal == nil || ready.ReadySeal.Revision != ready.Revision || ready.ReadySeal.RevisionSHA256 != ready.RevisionSHA256 {
		t.Fatalf("unexpected ready task: %#v", ready)
	}
	memory.replace(ready)
	idempotent, err := ReadyTask(memory.task, "planner", now.Add(3*time.Minute))
	if err != nil || idempotent.ReadySeal.ReadyAt != ready.ReadySeal.ReadyAt {
		t.Fatalf("ready transition was not idempotent: %#v %v", idempotent, err)
	}
	constraints := []string{"editing a ready task returns it to planned"}
	planned, changed, err := UpdateTask(memory.task, AuthoringPatch{Constraints: &constraints}, "planner", now.Add(4*time.Minute))
	if err != nil || !changed || planned.Status != model.TaskAuthoringPlanned || planned.ReadySeal != nil {
		t.Fatalf("ready edit did not reopen task: %#v %v %v", planned, changed, err)
	}
}

func TestAuthoringValidationAndRevisionGuardsAreRepositoryIndependent(t *testing.T) {
	if err := ValidateDraft(AuthoringDraft{
		Title:       "x",
		Objective:   "short",
		ADRRelation: model.TaskADRNoRequired,
	}); err == nil {
		t.Fatal("invalid draft was accepted")
	}
	now := time.Unix(10, 0).UTC()
	task, err := NewTask("gateway", "GTW-TSK180", authoringDraft(), "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRevision(task, task.Revision+1, task.RevisionSHA256); err == nil {
		t.Fatal("stale revision was accepted")
	}
	if err := CheckRevision(task, task.Revision, "different"); err == nil {
		t.Fatal("stale revision hash was accepted")
	}
	unchanged, changed, err := UpdateTask(task, AuthoringPatch{}, "planner", now.Add(time.Minute))
	if err != nil || changed || unchanged.Revision != task.Revision {
		t.Fatalf("empty patch was not a no-op: %#v %v %v", unchanged, changed, err)
	}
}

func TestAuthoringScopeAndExecutionUpdateRevision(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	draft := authoringDraft()
	draft.Execution = model.TaskExecutionHotfix
	draft.Scope = &model.TaskScope{Files: []string{"internal/service/task_authoring.go"}, Modules: []string{"gateway"}}
	task, err := NewTask("gateway", "GTW-TSK181", draft, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	if task.Execution != model.TaskExecutionHotfix || task.Scope == nil || len(task.Scope.Files) != 1 {
		t.Fatalf("created task lost scope/execution: %#v", task)
	}
	newScope := &model.TaskScope{Files: []string{"internal/service/hotfix_lifecycle.go"}, Modules: []string{"gateway"}}
	newExecution := model.TaskExecutionTrain
	updated, changed, err := UpdateTask(task, AuthoringPatch{Execution: &newExecution, Scope: newScope}, "planner", now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("scope/execution update failed: %#v %v %v", updated, changed, err)
	}
	if updated.Revision != 2 || updated.Execution != model.TaskExecutionTrain || updated.Scope == nil || updated.Scope.Files[0] != newScope.Files[0] {
		t.Fatalf("updated task lost scope/execution: %#v", updated)
	}
}
