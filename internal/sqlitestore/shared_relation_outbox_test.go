package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestSharedRelationInsertEnqueuesHubPublicationAtomically(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createdAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	created, err := db.CreateRelation(ctx, false, "example", model.RelationKindCorrects, "EXM-TSK1", "EXM-TSK2", "planner", createdAt)
	if err != nil || !created {
		t.Fatalf("relation create=%t err=%v", created, err)
	}
	identity := "example|corrects|EXM-TSK1|EXM-TSK2"
	rows, err := db.Shared.Query(ctx, `SELECT entity_type,entity_id,project_id,revision,kind,payload FROM hub_outbox WHERE id=?`, "relation-"+identity)
	if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 6 {
		t.Fatalf("relation outbox rows=%#v err=%v", rows, err)
	}
	row := rows.Rows[0]
	if row[0] != "relation" || row[1] != identity || row[2] != "example" || row[3] != int64(1) || row[4] != "relation-create" {
		t.Fatalf("relation outbox identity=%#v", row)
	}
	payload, ok := row[5].([]byte)
	if !ok {
		if text, textOK := row[5].(string); textOK {
			payload, ok = []byte(text), true
		}
	}
	if !ok {
		t.Fatalf("relation outbox payload has type %T", row[5])
	}
	var relation model.Relation
	if err := json.Unmarshal(payload, &relation); err != nil || model.ValidateRelation(relation) != nil || relation.Identity() != identity {
		t.Fatalf("relation outbox payload=%#v err=%v", relation, err)
	}
	if _, err := db.CreateRelation(ctx, false, "example", model.RelationKindCorrects, "EXM-TSK1", "EXM-TSK2", "other", createdAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_type='relation' AND entity_id=?`, identity)
	if err != nil || rows.Rows[0][0] != int64(1) {
		t.Fatalf("duplicate relation outbox count=%#v err=%v", rows.Rows, err)
	}
}

func TestSharedRelationPublicationMigrationBackfillsBoundedOutbox(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, sharedRelationHubOutboxMigrationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DROP TRIGGER shared_relations_hub_outbox_after_insert`); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_relations(project_id,kind,source_id,target_id,created_at,created_by) VALUES(?,?,?,?,?,?)`, "example", model.RelationKindCorrects, "EXM-TSK3", "EXM-TSK4", createdAt.Format(time.RFC3339Nano), "planner"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateSharedRelationsToHubOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateSharedRelationsToHubOutbox(ctx); err != nil {
		t.Fatalf("idempotent backfill: %v", err)
	}
	identity := "example|corrects|EXM-TSK3|EXM-TSK4"
	rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE id=?`, "relation-"+identity)
	if err != nil || rows.Rows[0][0] != int64(1) {
		t.Fatalf("backfilled relation outbox rows=%#v err=%v", rows.Rows, err)
	}
}
