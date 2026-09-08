package sqlitestore

import (
	"context"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestTSK538LegacySharedBridgePreservesDataAndBothSequenceDirections(t *testing.T) {
	stateDir := t.TempDir()
	sharedPath, _ := Paths(stateDir)
	ctx := context.Background()
	shared, err := upstream.Open(engineConfig(Config{})(sharedPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyTSK538SharedLegacyPrefix(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_project_identifiers VALUES(?,?,?,?,?,?,?)`, "example", "EXM", 4, 8, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_task_sequences VALUES(?,?,?)`, "example", "EXM", 9); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_adr_sequences VALUES(?,?,?)`, "example", "EXM", 11); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_task_sequences VALUES(?,?,?)`, "solo", "SOL", 17); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_adr_sequences VALUES(?,?,?)`, "solo", "SOL", 19); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"id":"EXM-ADR1","project_id":"example","revision":3,"created_by":"planner","last_reason":"seed"}`)
	if _, err := shared.Exec(ctx, `INSERT INTO shared_adrs VALUES(?,?,?,?)`, "EXM-ADR1", 99, payload, "2026-09-08T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Shared.Query(ctx, `SELECT version,name FROM schema_migrations WHERE version=?`, sharedBridgeVersion)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][1] != sharedBridgeName {
		t.Fatalf("bridge marker=%#v err=%v", rows.Rows, err)
	}
	assertFinalSharedSchema(t, db.Shared)
	rows, err = db.Shared.Query(ctx, `SELECT project_id,next_number FROM shared_entity_sequences WHERE entity_type='task' ORDER BY project_id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 2 || rows.Rows[0][0] != "example" || rows.Rows[0][1] != int64(9) || rows.Rows[1][0] != "solo" || rows.Rows[1][1] != int64(17) {
		t.Fatalf("task high-water=%#v", rows.Rows)
	}
	rows, err = db.Shared.Query(ctx, `SELECT project_id,next_number FROM shared_entity_sequences WHERE entity_type='adr' ORDER BY project_id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 2 || rows.Rows[0][1] != int64(11) || rows.Rows[1][1] != int64(19) {
		t.Fatalf("ADR high-water=%#v", rows.Rows)
	}
	rows, err = db.Shared.Query(ctx, `SELECT revision,changed_fields,payload FROM shared_entity_revisions WHERE entity_id=?`, "EXM-ADR1")
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(3) || migrationPayloadString(rows.Rows[0][2]) != string(payload) {
		t.Fatalf("ADR history=%#v err=%v", rows.Rows, err)
	}
	if migrationPayloadString(rows.Rows[0][1]) != `["migration"]` {
		t.Fatalf("changed_fields=%#v", rows.Rows[0][1])
	}
	if _, err := db.Shared.Exec(ctx, `UPDATE shared_adrs SET payload=? WHERE id=?`, []byte("changed"), "EXM-ADR1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertMigrationMarker(t, reopened.Local, localBaselineVersion, localBaselineName)
}

func TestTSK538HistoricalDeployedSharedBridgePreservesObsoleteTables(t *testing.T) {
	stateDir := t.TempDir()
	sharedPath, _ := Paths(stateDir)
	ctx := context.Background()
	shared, err := upstream.Open(engineConfig(Config{})(sharedPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := applyTSK538DeployedSharedHistory(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_agents VALUES(?,?,?,?)`, "agent-1", 1, []byte("agent"), "now"); err != nil {
		t.Fatal(err)
	}
	if _, err := shared.Exec(ctx, `INSERT INTO shared_journal_sequences VALUES(?,?,?)`, "p", "P", 4); err != nil {
		t.Fatal(err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertFinalSharedSchema(t, db.Shared)
	assertObjects(t, db.Shared, "table", []string{"shared_agents", "shared_watcher_guides", "shared_journal_sequences", "shared_journal_supersessions", "shared_integration_operations"})
	rows, err := db.Shared.Query(ctx, `SELECT payload FROM shared_agents WHERE id='agent-1'`)
	if err != nil || len(rows.Rows) != 1 || migrationPayloadString(rows.Rows[0][0]) != "agent" {
		t.Fatalf("obsolete data=%#v err=%v", rows.Rows, err)
	}
	rows, err = db.Shared.Query(ctx, `SELECT name FROM schema_migrations WHERE version=?`, sharedBridgeVersion)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != sharedBridgeName {
		t.Fatalf("bridge marker=%#v err=%v", rows.Rows, err)
	}
}
