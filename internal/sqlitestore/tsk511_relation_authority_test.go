package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func tsk511SharedRelationColumns() []string {
	return []string{"project_id", "kind", "source_id", "target_id", "created_at", "created_by"}
}

func TestTSK511Gate20RelationAuthorityShape(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	assertColumns(t, db.Shared, "shared_relations", tsk511SharedRelationColumns())
	assertColumns(t, db.Local, "local_relations", tsk511SharedRelationColumns())
	assertObjects(t, db.Shared, "index", []string{"shared_relations_target_idx"})
	assertObjects(t, db.Local, "index", []string{"local_relations_target_idx"})

	// Gate20: no relation authority stores a copied target title, and no second
	// relation-shaped table exists beside the two canonical stores.
	for _, table := range []string{"shared_relations", "local_relations"} {
		rows, err := db.Shared.Query(ctx, `SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows.Rows {
			switch row[0].(string) {
			case "title", "target_title", "target_key", "label", "name":
				t.Fatalf("%s stores a copied title column %q", table, row[0])
			}
		}
	}
	rows, err := db.Shared.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%relation%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "shared_relations" {
		t.Fatalf("shared relation-shaped tables=%#v", rows.Rows)
	}
	rows, err = db.Local.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%relation%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "local_relations" {
		t.Fatalf("local relation-shaped tables=%#v", rows.Rows)
	}

	// Gate20: the legacy Task/ADR scalar references remain their own owners and
	// are not duplicated into relation authority.
	assertColumns(t, db.Shared, "shared_tasks", []string{"id", "revision", "payload", "updated_at"})
	assertColumns(t, db.Shared, "shared_adrs", []string{"id", "revision", "payload", "updated_at"})
}

func TestTSK511RelationStoreIdempotenceAndDirections(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 11, 23, 0, 0, time.UTC)

	created, err := db.CreateRelation(ctx, false, "example", "authority", "EXM-TSK1", "EXM-ADR1", "planner", now)
	if err != nil || !created {
		t.Fatalf("first create created=%v err=%v", created, err)
	}
	repeat, err := db.CreateRelation(ctx, false, "example", "authority", "EXM-TSK1", "EXM-ADR1", "planner", now)
	if err != nil || repeat {
		t.Fatalf("repeat create created=%v err=%v", repeat, err)
	}
	if _, err := db.CreateRelation(ctx, false, "example", "authority", "EXM-ADR1", "EXM-RUL1", "planner", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRelation(ctx, true, "example", "concerns", "EXM-PMT1", "EXM-TSK1", "planner", now); err != nil {
		t.Fatal(err)
	}

	outgoing, err := db.ListRelationPage(ctx, false, "example", "EXM-TSK1", []string{"authority"}, RelationDirectionOutgoing, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outgoing) != 1 || outgoing[0].Other != "EXM-ADR1" || outgoing[0].Direction != RelationDirectionOutgoing {
		t.Fatalf("outgoing page=%#v", outgoing)
	}
	incoming, err := db.ListRelationPage(ctx, false, "example", "EXM-TSK1", []string{"authority"}, RelationDirectionIncoming, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(incoming) != 0 {
		t.Fatalf("incoming page=%#v", incoming)
	}
	both, err := db.ListRelationPage(ctx, false, "example", "EXM-ADR1", []string{"authority"}, RelationDirectionBoth, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 || both[0].Direction != RelationDirectionOutgoing || both[1].Direction != RelationDirectionIncoming {
		t.Fatalf("both page=%#v", both)
	}
	local, err := db.ListRelationPage(ctx, true, "example", "EXM-PMT1", []string{"concerns"}, RelationDirectionOutgoing, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != 1 || local[0].Other != "EXM-TSK1" {
		t.Fatalf("local page=%#v", local)
	}
	if shared, err := db.CountRelations(ctx, false); err != nil || shared != 2 {
		t.Fatalf("shared relation count=%d err=%v", shared, err)
	}
	if localCount, err := db.CountRelations(ctx, true); err != nil || localCount != 1 {
		t.Fatalf("local relation count=%d err=%v", localCount, err)
	}
	if _, err := db.ListRelationPage(ctx, false, "example", "EXM-TSK1", nil, RelationDirectionOutgoing, "", "", "", RelationListMaxRows+1); err == nil {
		t.Fatal("over-bound relation list limit was accepted")
	}
	if _, err := db.ListRelationPage(ctx, false, "example", "EXM-TSK1", nil, "sideways", "", "", "", 10); err == nil {
		t.Fatal("invalid direction was accepted")
	}
}

func TestTSK511RelationStoreKeysetPagination(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 11, 23, 0, 0, time.UTC)
	for _, kind := range []string{"authority", "corrects"} {
		for i := 1; i <= 3; i++ {
			target := "EXM-TSK1"
			if kind == "authority" {
				target = "EXM-ADR" + string(rune('0'+i))
			} else {
				target = "EXM-TSK" + string(rune('0'+i))
			}
			if _, err := db.CreateRelation(ctx, false, "example", kind, "EXM-TSK9", target, "planner", now); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	afterKind, afterOther, afterDirection := "", "", ""
	for page := 0; page < 10; page++ {
		rows, err := db.ListRelationPage(ctx, false, "example", "EXM-TSK9", nil, RelationDirectionOutgoing, afterKind, afterOther, afterDirection, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			key := row.Kind + "|" + row.Other
			if seen[key] {
				t.Fatalf("relation %s repeated across keyset pages", key)
			}
			seen[key] = true
		}
		last := rows[len(rows)-1]
		afterKind, afterOther, afterDirection = last.Kind, last.Other, last.Direction
	}
	if len(seen) != 6 {
		t.Fatalf("keyset pagination returned %d relations, want 6: %#v", len(seen), seen)
	}
}

func TestTSK511RelationSugarIsAtomicWithEntityCreate(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 11, 23, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "id": "EXM-ADR1", "project_id": "example", "title": "First ADR"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-ADR1", 1, payload, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	create := func(operationID string) (SharedMutationReceipt, error) {
		receipt, _, _, err := db.CommitSharedLifecycleCreate(ctx, SharedLifecycleCreate{
			OperationID:         operationID,
			EntityType:          "adr",
			ProjectID:           "example",
			ProjectCode:         "EXM",
			InitialNextNumber:   2,
			Kind:                "adr-create",
			HistoryMutationKind: "create",
			Actor:               "planner",
			Reason:              "create",
			ChangedFields:       []string{"create"},
			CreatedAt:           now,
			BuildPayload: func(id string) ([]byte, error) {
				return json.Marshal(map[string]any{"schema_version": 1, "id": id, "project_id": "example", "title": "Created"})
			},
			ExtraStatements: func(entityID string) ([]upstream.Statement, error) {
				return []upstream.Statement{RelationInsertStatement(false, "example", "supersedes", entityID, "EXM-ADR1", now.Format(time.RFC3339Nano), "planner")}, nil
			},
		})
		return receipt, err
	}
	receipt, err := create("tsk511-atomic-create")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Shared.Query(ctx, `SELECT kind,source_id,target_id FROM shared_relations`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "supersedes" || rows.Rows[0][1] != receipt.EntityID || rows.Rows[0][2] != "EXM-ADR1" {
		t.Fatalf("atomic relation rows=%#v", rows.Rows)
	}

	if _, err := db.Shared.Exec(ctx, `CREATE TRIGGER tsk511_relation_fault BEFORE INSERT ON shared_relations BEGIN SELECT RAISE(ABORT,'injected relation fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := create("tsk511-atomic-fault"); err == nil {
		t.Fatal("injected relation fault did not fail the create")
	}
	if _, err := db.Shared.Exec(ctx, `DROP TRIGGER tsk511_relation_fault`); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		query string
		want  int64
	}{
		{`SELECT COUNT(*) FROM shared_adrs`, 2},
		{`SELECT COUNT(*) FROM shared_relations`, 1},
		{`SELECT COUNT(*) FROM hub_outbox WHERE id='tsk511-atomic-fault'`, 0},
		{`SELECT COUNT(*) FROM shared_entity_revisions WHERE entity_id NOT IN ('EXM-ADR1')`, 1},
	} {
		rows, err := db.Shared.Query(ctx, probe.query)
		if err != nil {
			t.Fatal(err)
		}
		if count, _ := rows.Rows[0][0].(int64); count != probe.want {
			t.Fatalf("%s = %d, want %d", probe.query, count, probe.want)
		}
	}
}

func TestTSK511MaterializesReviewedCorrectionRelation(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := db.Shared.Exec(ctx, `DELETE FROM schema_migrations WHERE version=?`, sharedRelationMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_relations`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 18, 11, 23, 0, 0, time.UTC)
	for _, id := range []string{"GTW-TSK585", "GTW-TSK521"} {
		payload, err := json.Marshal(map[string]any{"schema_version": 1, "id": id, "project_id": "gpt-tunnel-gateway", "title": "Retained " + id})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, id, 1, payload, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Shared.Query(ctx, `SELECT project_id,kind,source_id,target_id,created_by FROM shared_relations`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "gpt-tunnel-gateway" || rows.Rows[0][1] != "corrects" || rows.Rows[0][2] != "GTW-TSK585" || rows.Rows[0][3] != "GTW-TSK521" || rows.Rows[0][4] != "migration" {
		t.Fatalf("materialized correction relation=%#v", rows.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err = db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_relations`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := rows.Rows[0][0].(int64); count != 1 {
		t.Fatalf("reopen duplicated the materialized relation: %d", count)
	}
}

func TestTSK511MaterializationIsBoundedToRetainedEntities(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Shared.Query(context.Background(), `SELECT COUNT(*) FROM shared_relations`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := rows.Rows[0][0].(int64); count != 0 {
		t.Fatalf("fresh store materialized an unrelated relation: %d", count)
	}
}
