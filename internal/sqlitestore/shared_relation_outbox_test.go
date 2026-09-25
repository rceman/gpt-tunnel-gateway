package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	rows, err := db.Shared.Query(ctx, `SELECT entity_type,entity_id,project_id,revision,kind,payload,typeof(payload) FROM hub_outbox WHERE id=?`, "relation-"+identity)
	if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 7 {
		t.Fatalf("relation outbox rows=%#v err=%v", rows, err)
	}
	row := rows.Rows[0]
	if row[0] != "relation" || row[1] != identity || row[2] != "example" || row[3] != int64(1) || row[4] != "relation-create" || row[6] != "blob" {
		t.Fatalf("relation outbox identity or storage class=%#v", row)
	}
	payload, ok := row[5].([]byte)
	if !ok {
		t.Fatalf("relation outbox payload has type %T, want BLOB bytes", row[5])
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

func TestSharedRelationOutboxBlobMigrationPreservesAndReadsLegacyTextRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Shared.Exec(ctx, `DROP TRIGGER IF EXISTS shared_relations_hub_outbox_after_insert`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, sharedRelationOutboxMigration().Statements[0].SQL); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC)
	created, err := db.CreateRelation(ctx, false, "example", model.RelationKindCorrects, "EXM-TSK3", "EXM-TSK4", "planner", createdAt)
	if err != nil || !created {
		t.Fatalf("legacy relation create=%t err=%v", created, err)
	}
	legacyID := "relation-example|corrects|EXM-TSK3|EXM-TSK4"
	rows, err := db.Shared.Query(ctx, `SELECT typeof(payload),payload FROM hub_outbox WHERE id=?`, legacyID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "text" {
		t.Fatalf("legacy relation payload storage=%#v err=%v", rows.Rows, err)
	}
	legacyText, ok := rows.Rows[0][1].(string)
	if !ok {
		t.Fatalf("legacy relation payload type=%T, want TEXT string", rows.Rows[0][1])
	}
	pending, err := db.PendingOutbox(ctx, 10)
	foundLegacy := false
	for _, entry := range pending {
		if entry.ID == legacyID {
			foundLegacy = true
			if !bytes.Equal(entry.Payload, []byte(legacyText)) {
				t.Fatalf("normalized TEXT payload=%q want original bytes %q", entry.Payload, legacyText)
			}
		}
	}
	if err != nil || !foundLegacy {
		t.Fatalf("PendingOutbox did not return legacy relation row: entries=%#v err=%v", pending, err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM schema_migrations WHERE version=?`, sharedRelationOutboxBlobMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := applySharedMigrations(ctx, db.Shared); err != nil {
		t.Fatalf("apply relation payload BLOB migration: %v", err)
	}
	rows, err = db.Shared.Query(ctx, `SELECT typeof(payload) FROM hub_outbox WHERE id=?`, legacyID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "text" {
		t.Fatalf("upgrade rewrote the existing TEXT row: rows=%#v err=%v", rows.Rows, err)
	}
	created, err = db.CreateRelation(ctx, false, "example", model.RelationKindCorrects, "EXM-TSK5", "EXM-TSK6", "planner", createdAt.Add(time.Minute))
	if err != nil || !created {
		t.Fatalf("canonical relation create=%t err=%v", created, err)
	}
	rows, err = db.Shared.Query(ctx, `SELECT typeof(payload),payload FROM hub_outbox WHERE id=?`, "relation-example|corrects|EXM-TSK5|EXM-TSK6")
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "blob" {
		t.Fatalf("upgraded relation trigger payload storage=%#v err=%v", rows.Rows, err)
	}
	if _, ok := rows.Rows[0][1].([]byte); !ok {
		t.Fatalf("upgraded relation payload type=%T, want BLOB bytes", rows.Rows[0][1])
	}
}

func TestPendingOutboxRejectsUnsupportedPayloadStorageType(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?)`, "unsupported-payload", "relation", "example|corrects|EXM-TSK7|EXM-TSK8", 1, "relation-create", int64(8675309), "0001-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	_, err = db.PendingOutbox(ctx, 10)
	var decodeErr OutboxRowDecodeError
	if !errors.As(err, &decodeErr) || decodeErr.Field != "payload" || !strings.Contains(err.Error(), "int64") || strings.Contains(err.Error(), "8675309") {
		t.Fatalf("unsupported payload error=%v decode=%#v", err, decodeErr)
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
