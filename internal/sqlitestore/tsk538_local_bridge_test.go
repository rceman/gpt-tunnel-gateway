package sqlitestore

import (
	"context"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestTSK538LegacyLocalBridgePreservesInterSessionAndProjectData(t *testing.T) {
	stateDir := t.TempDir()
	_, localPath := Paths(stateDir)
	ctx := context.Background()
	local, err := upstream.Open(upstream.Config{Path: localPath})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyTSK538LocalLegacyHistory(ctx, local); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Exec(ctx, `INSERT INTO local_events(id,kind,payload,recorded_at,project_id) VALUES(?,?,?,?,?)`, "event-1", "test", []byte("event"), "now", "project"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Exec(ctx, `INSERT INTO local_inter_session_messages VALUES(?,?,?,?,?,?,?,?,?)`, "msg-1", "project", "source", "target", "topic", "body", []byte("tags"), "created", "expires"); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertFinalLocalSchema(t, db.Local)
	rows, err := db.Local.Query(ctx, `SELECT version,name FROM schema_migrations WHERE version=?`, localBridgeVersion)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][1] != localBridgeName {
		t.Fatalf("bridge marker=%#v err=%v", rows.Rows, err)
	}
	rows, err = db.Local.Query(ctx, `SELECT project_id FROM local_events WHERE id='event-1'`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "project" {
		t.Fatalf("project data=%#v err=%v", rows.Rows, err)
	}
	rows, err = db.Local.Query(ctx, `SELECT body FROM local_inter_session_messages WHERE id='msg-1'`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "body" {
		t.Fatalf("legacy data=%#v err=%v", rows.Rows, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err = reopened.Local.Query(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, localBridgeVersion)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(1) {
		t.Fatalf("reopen bridge count=%#v err=%v", rows.Rows, err)
	}
}
