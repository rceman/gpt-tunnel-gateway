package sqlitestore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/go-sqlite-store/migrate"
	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK531SummaryMigrationPreservesLegacyPayloadAndAddsOneRevision(t *testing.T) {
	ctx := context.Background()
	db, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/shared.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.Apply(ctx, db, []migrate.Migration{sharedBaselineMigration()}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	legacy := summaryMigrationFixture("GTW-TSK433", "No short sentence here.", oldTime)
	legacy.Summary = ""
	legacy.RevisionSHA256, err = model.HashTaskAuthoring(legacy)
	if err != nil {
		t.Fatal(err)
	}
	oldPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, legacy.ID, 1, oldPayload, oldTime.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	migration, err := sharedTaskSummaryMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, db, []migrate.Migration{migration}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT revision,payload FROM shared_tasks WHERE id=?`, legacy.ID)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("current row=%#v err=%v", rows.Rows, err)
	}
	if rows.Rows[0][0] != int64(2) {
		t.Fatalf("current revision=%v", rows.Rows[0][0])
	}
	if string(rows.Rows[0][1].([]byte)) == string(oldPayload) {
		t.Fatal("summary migration did not create a new payload")
	}
	history, err := db.Query(ctx, `SELECT revision,mutation_kind,changed_fields,payload FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? ORDER BY revision`, legacy.ID)
	if err != nil || len(history.Rows) != 2 {
		t.Fatalf("history=%#v err=%v", history.Rows, err)
	}
	if string(history.Rows[0][3].([]byte)) != string(oldPayload) || history.Rows[0][1] != "migration" || string(history.Rows[0][2].([]byte)) != `["legacy"]` {
		t.Fatalf("legacy evidence=%#v", history.Rows[0])
	}
	if count, err := db.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_id=?`, legacy.ID); err != nil || count.Rows[0][0] != int64(1) {
		t.Fatalf("summary outbox=%#v err=%v", count.Rows, err)
	}
}

func TestTSK531SummaryMigrationUsesSentenceAndNoOpPaths(t *testing.T) {
	ctx := context.Background()
	db, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/shared.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.Apply(ctx, db, []migrate.Migration{sharedBaselineMigration()}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sentence := summaryMigrationFixture("GTW-TSK900", "First complete sentence. Additional objective text.", now)
	sentence.Summary = ""
	sentence.RevisionSHA256, err = model.HashTaskAuthoring(sentence)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []model.TaskAuthoring{
		sentence,
		summaryMigrationFixture("GTW-TSK901", "Already valid summary.", now),
	} {
		if _, err := db.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, task.Revision, mustJSON(t, task), task.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := sharedTaskSummaryMigration(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(migration.Statements) == 0 {
		t.Fatal("summary migration has no statements")
	}
	if !strings.Contains(string(migration.Statements[0].SQL), "UPDATE") {
		t.Fatalf("unexpected first migration statement: %s", migration.Statements[0].SQL)
	}
	if _, err := db.Exec(ctx, `DELETE FROM shared_tasks WHERE id=?`, "GTW-TSK900"); err != nil {
		t.Fatal(err)
	}
	noOp, err := sharedTaskSummaryMigration(ctx, db)
	if err != nil || len(noOp.Statements) != 1 || noOp.Statements[0].SQL != "SELECT 1" {
		t.Fatalf("already-summarized no-op=%#v err=%v", noOp, err)
	}
}

func TestTSK531SummaryMigrationRejectsConflictingHistory(t *testing.T) {
	ctx := context.Background()
	db, err := upstream.Open(upstream.Config{Path: t.TempDir() + "/shared.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.Apply(ctx, db, []migrate.Migration{sharedBaselineMigration()}, migrate.Options{}); err != nil {
		t.Fatal(err)
	}
	task := summaryMigrationFixture("GTW-TSK433", "legacy objective.", time.Now().UTC())
	payload := mustJSON(t, task)
	if _, err := db.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, 1, payload, task.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "task", task.ID, task.ProjectID, 1, "migration", "test", "wrong", []byte(`["legacy"]`), []byte(`{"different":true}`), task.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedTaskSummaryMigration(ctx, db); err == nil {
		t.Fatal("conflicting immutable history was accepted")
	}
}

func summaryMigrationFixture(id, objective string, now time.Time) model.TaskAuthoring {
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: id, ProjectID: "example", Revision: 1, Title: "Migration fixture", Objective: objective, AcceptanceCriteria: []string{"preserve"}, ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringPlanned, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now}
	task.Summary = "Already valid summary."
	task.RevisionSHA256, _ = model.HashTaskAuthoring(task)
	return task
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
