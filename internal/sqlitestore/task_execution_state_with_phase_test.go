package sqlitestore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk683State() model.TaskExecutionState {
	return model.TaskExecutionState{
		TaskID: "GTW-TSK660", ProjectID: "example",
		TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionIntegrated, Stage: "code",
		Worktree: "WT-TSK660-f09d0834",
		BaseHead: "f09d0834d125362d63e1d12b361ad34c63e50d8d",
		Head:     "f09d0834d125362d63e1d12b361ad34c63e50d8d",
		Branch:   "task/GTW-TSK660-slug", Agent: "gtw-worker",
		ExecutionRevision: 1, UpdatedAt: time.Now().UTC(),
	}
}

func TestCreateTaskExecutionStateWithPhaseIsAtomic(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	state := tsk683State()
	phase := TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  1,
		Stage:              "integration",
		Status:             model.TaskExecutionIntegrated,
		Head:               "899abc90157ee5b4e8b6b27e4284e0c841ccbb17",
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "integration",
		Decision:           "accept",
		Comment:            "task-pre-execution-reconciliation:{\"schema_version\":1}",
		CreatedAt:          state.UpdatedAt,
	}
	if err := db.CreateTaskExecutionStateWithPhase(ctx, state, phase); err != nil {
		t.Fatalf("create state with phase: %v", err)
	}
	read, found, err := db.ReadTaskExecutionState(ctx, state.ProjectID, state.TaskID)
	if err != nil || !found || read.Status != model.TaskExecutionIntegrated {
		t.Fatalf("state read found=%v status=%q err=%v", found, read.Status, err)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, state.ProjectID, state.TaskID, "integration")
	if err != nil || len(phases) != 1 || phases[0].Head != phase.Head {
		t.Fatalf("phases=%v err=%v", phases, err)
	}

	// A mismatched phase must fail before writing anything.
	other := tsk683State()
	other.TaskID = "GTW-TSK900"
	other.Worktree = "WT-TSK900-f09d0834"
	other.Branch = "task/GTW-TSK900-slug"
	bad := phase
	bad.TaskID = "GTW-TSK901"
	if err := db.CreateTaskExecutionStateWithPhase(ctx, other, bad); err == nil {
		t.Fatal("mismatched phase was accepted")
	}
	if _, found, err := db.ReadTaskExecutionState(ctx, other.ProjectID, other.TaskID); err != nil || found {
		t.Fatalf("orphan state persisted after rejected create: found=%v err=%v", found, err)
	}

	// A duplicate create fails closed.
	if err := db.CreateTaskExecutionStateWithPhase(ctx, state, phase); err == nil {
		t.Fatal("duplicate create did not fail")
	}
}
