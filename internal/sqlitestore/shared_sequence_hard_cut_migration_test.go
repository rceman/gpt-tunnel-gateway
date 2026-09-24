package sqlitestore

import (
	"context"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestMigrateLegacySharedSequencesPreservesHighWaterAndDropsSources(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Shared.Batch(ctx, []upstream.Statement{
		{SQL: `DROP TABLE shared_project_identifiers`},
		{SQL: `CREATE TABLE shared_project_identifiers (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_task_number INTEGER NOT NULL, next_adr_number INTEGER NOT NULL, next_rule_number INTEGER NOT NULL, next_journal_number INTEGER NOT NULL, next_train_number INTEGER NOT NULL)`},
		{SQL: `CREATE TABLE shared_task_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_task_number INTEGER NOT NULL)`},
		{SQL: `CREATE TABLE shared_adr_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_adr_number INTEGER NOT NULL)`},
		{SQL: `INSERT INTO shared_task_sequences(project_id,project_code,next_task_number) VALUES('example','EXM',25)`},
		{SQL: `INSERT INTO shared_adr_sequences(project_id,project_code,next_adr_number) VALUES('example','EXM',8)`},
		{SQL: `INSERT INTO shared_project_identifiers(project_id,project_code,next_task_number,next_adr_number,next_rule_number,next_journal_number,next_train_number) VALUES('example','EXM',40,6,10,13,1)`},
		{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES('task','example','EXM',30)`},
		{SQL: `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES('train','example','EXM',9)`},
		{SQL: `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, Args: []any{sharedSequenceHardCutMigrationID}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateLegacySharedSequences(ctx); err != nil {
		t.Fatal(err)
	}
	for entityType, want := range map[string]int64{"task": 40, "adr": 8, "rule": 10, "journal": 13} {
		code, next, found, err := db.ReadSharedSequence(ctx, entityType, "example")
		if err != nil || !found || code != "EXM" || next != want {
			t.Fatalf("%s sequence=(%q,%d,%t) err=%v, want (%q,%d,true)", entityType, code, next, found, err, "EXM", want)
		}
	}
	for _, table := range []string{"shared_task_sequences", "shared_adr_sequences"} {
		exists, err := db.sharedTableExists(ctx, table)
		if err != nil || exists {
			t.Fatalf("legacy table %q remains: exists=%t err=%v", table, exists, err)
		}
	}
	trainRows, err := db.Shared.Query(ctx, `SELECT entity_type FROM shared_entity_sequences WHERE entity_type='train' AND project_id=?`, "example")
	if err != nil || len(trainRows.Rows) != 0 {
		t.Fatalf("retired Train sequence remains: rows=%v err=%v", trainRows.Rows, err)
	}
	columns, err := db.sharedProjectIdentifierColumns(ctx)
	if err != nil || len(columns) != 2 {
		t.Fatalf("retired identifier counters remain: columns=%v err=%v", columns, err)
	}
	identities, err := db.Shared.Query(ctx, `SELECT project_id,project_code FROM shared_project_identifiers`)
	if err != nil || len(identities.Rows) != 1 || identities.Rows[0][0] != "example" || identities.Rows[0][1] != "EXM" {
		t.Fatalf("project identity was not preserved: rows=%v err=%v", identities.Rows, err)
	}
	if err := db.MigrateLegacySharedSequences(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
