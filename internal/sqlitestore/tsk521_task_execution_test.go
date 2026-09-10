package sqlitestore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk521ExecutionState() model.TaskExecutionState {
	return model.TaskExecutionState{
		TaskID: "EXM-TSK521", ProjectID: "example", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionDispatched, Stage: "code", Worktree: "WT-TSK521-bbbbbbbb",
		BaseHead: strings.Repeat("b", 40), Head: strings.Repeat("b", 40), Branch: "task/EXM-TSK521-task",
		Agent: "EXM-CODER", ExecutionRevision: 1, UpdatedAt: time.Now().UTC(),
	}
}

func TestTSK521TaskExecutionStorageRejectsCorruption(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	state := tsk521ExecutionState()
	if err := db.CreateTaskExecutionState(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.ReadTaskExecutionState(ctx, state.ProjectID, state.TaskID)
	if err != nil || !found || got.TaskRevision != state.TaskRevision {
		t.Fatalf("read state=%#v found=%v err=%v", got, found, err)
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_task_execution_states SET task_revision_sha256=? WHERE task_id=?`, strings.Repeat("A", 64), state.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ReadTaskExecutionState(ctx, state.ProjectID, state.TaskID); err == nil {
		t.Fatal("corrupted Task hash was accepted")
	}
}

func TestTSK521SharedExecutionMigrationIsSingleDeterministicMarker(t *testing.T) {
	for i := 0; i < 2; i++ {
		db, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Shared.Query(context.Background(), `SELECT version,name FROM schema_migrations WHERE version=?`, sharedTaskExecutionMigrationVersion)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][1] != sharedTaskExecutionMigrationName {
			t.Fatalf("execution marker=%#v err=%v", rows.Rows, err)
		}
		db.Close()
	}
}
