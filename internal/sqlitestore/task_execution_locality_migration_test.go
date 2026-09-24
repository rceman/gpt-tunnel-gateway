package sqlitestore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskExecutionSharedToLocalMigrationPreservesBoundedState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Local.Exec(ctx, `DELETE FROM local_upgrade_migrations WHERE migration_id=?`, taskExecutionLocalityMigrationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, taskExecutionLocalityMigrationID); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []migrate.Migration{sharedTaskExecutionMigration(), sharedTaskExecutionPhasesMigration(), sharedTaskExecutionVerificationMigration()} {
		if _, err := db.Shared.Batch(ctx, migration.Statements); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	state := model.TaskExecutionState{
		ProjectID: "example", TaskID: "EXM-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
		Status: model.TaskExecutionInProgress, Stage: "code", Worktree: "WT-TSK1-bbbbbbbb", BaseHead: strings.Repeat("b", 40),
		Head: strings.Repeat("b", 40), Branch: "task/EXM-TSK1-cutover", Agent: "EXM-CODER", ExecutionRevision: 1, UpdatedAt: now,
	}
	if err := model.ValidateTaskExecutionState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_execution_states(task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		state.TaskID, state.ProjectID, state.TaskRevision, state.TaskRevisionSHA256, state.Status, state.Stage, state.Worktree, state.BaseHead, state.Head, state.Branch, state.Agent, state.ExecutionRevision, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	phase := TaskExecutionPhase{
		TaskID:             state.TaskID,
		ProjectID:          state.ProjectID,
		ExecutionRevision:  1,
		Stage:              "code",
		Status:             model.TaskExecutionInProgress,
		Head:               state.Head,
		Branch:             state.Branch,
		TaskRevisionSHA256: state.TaskRevisionSHA256,
		EventKind:          "submission",
		CreatedAt:          now,
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_execution_phases(task_id,project_id,execution_revision,stage,status,head_sha,branch,task_revision_sha256,event_kind,decision,comment,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		phase.TaskID, phase.ProjectID, phase.ExecutionRevision, phase.Stage, phase.Status, phase.Head, phase.Branch, phase.TaskRevisionSHA256, phase.EventKind, phase.Decision, phase.Comment, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	receipt := model.TaskExecutionVerification{
		ProjectID: state.ProjectID, TaskID: state.TaskID, OperationID: "EXM-OPR1", TaskRevisionSHA256: state.TaskRevisionSHA256,
		BaseHead: state.BaseHead, CandidateHead: strings.Repeat("c", 40), CandidateTree: strings.Repeat("d", 40), Branch: state.Branch,
		GateProfileSHA256: strings.Repeat("e", 64), Outcome: model.TaskExecutionVerificationFailed, Error: "gate failed", TaskRevision: 1,
		AttemptRevision: 1, CodeReviewID: 1, Gates: []model.CompletionGateResult{{ID: "go-test", ExitCode: 1}},
		StartedAt: now, CompletedAt: now.Add(time.Second),
	}
	if err := model.ValidateTaskExecutionVerification(receipt); err != nil {
		t.Fatal(err)
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_execution_verifications(project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		receipt.ProjectID, receipt.TaskID, receipt.OperationID, receipt.AttemptRevision, receipt.Outcome, string(rawReceipt), now.Add(time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := db.copyTaskExecutionTable(ctx, taskExecutionTables[0]); err != nil {
		t.Fatalf("partial pre-crash copy: %v", err)
	}
	if err := db.setLocalUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID, "copied"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateTaskExecutionStateToLocal(ctx); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.ReadTaskExecutionState(ctx, state.ProjectID, state.TaskID)
	if err != nil || !found || got.Status != state.Status || got.Worktree != state.Worktree {
		t.Fatalf("migrated state=%#v found=%v err=%v", got, found, err)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, state.ProjectID, state.TaskID, "code")
	if err != nil || len(phases) != 1 || phases[0].EventKind != phase.EventKind {
		t.Fatalf("migrated phases=%#v err=%v", phases, err)
	}
	gotReceipt, found, err := db.ReadLatestTaskExecutionVerification(ctx, state.ProjectID, state.TaskID)
	if err != nil || !found || gotReceipt.OperationID != receipt.OperationID {
		t.Fatalf("migrated verification=%#v found=%v err=%v", gotReceipt, found, err)
	}
	for _, table := range taskExecutionTables {
		exists, err := db.sharedTableExists(ctx, table.sharedTable)
		if err != nil || exists {
			t.Fatalf("Shared source %q remains after cutover: exists=%v err=%v", table.sharedTable, exists, err)
		}
	}
	if err := db.MigrateTaskExecutionStateToLocal(ctx); err != nil {
		t.Fatalf("restart replay: %v", err)
	}
	marker, err := db.localUpgradeMigrationState(ctx, taskExecutionLocalityMigrationID)
	if err != nil || marker != "complete" {
		t.Fatalf("migration marker=%q err=%v", marker, err)
	}
}

func TestTaskExecutionLocalityMigrationRejectsConflictingLocalCopy(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Local.Exec(ctx, `DELETE FROM local_upgrade_migrations WHERE migration_id=?`, taskExecutionLocalityMigrationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, taskExecutionLocalityMigrationID); err != nil {
		t.Fatal(err)
	}
	for _, migration := range []migrate.Migration{sharedTaskExecutionMigration(), sharedTaskExecutionPhasesMigration(), sharedTaskExecutionVerificationMigration()} {
		if _, err := db.Shared.Batch(ctx, migration.Statements); err != nil {
			t.Fatal(err)
		}
	}
	state := tsk521ExecutionState()
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_task_execution_states(task_id,project_id,task_revision,task_revision_sha256,status,stage,worktree,base_head_sha,head_sha,branch,agent,execution_revision,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		state.TaskID, state.ProjectID, state.TaskRevision, state.TaskRevisionSHA256, state.Status, state.Stage, state.Worktree, state.BaseHead, state.Head, state.Branch, state.Agent, state.ExecutionRevision, state.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	conflict := state
	conflict.Agent = "EXM-OTHER"
	payloadState := conflict
	if err := db.CreateTaskExecutionState(ctx, payloadState); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateTaskExecutionStateToLocal(ctx); err == nil {
		t.Fatal("conflicting Local TaskExecution state was accepted")
	}
	exists, err := db.sharedTableExists(ctx, "shared_task_execution_states")
	if err != nil || !exists {
		t.Fatalf("Shared source lost after rejected migration: exists=%v err=%v", exists, err)
	}
}
