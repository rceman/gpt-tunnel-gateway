package sqlitestore

import (
	"context"
	"testing"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func migrationPayloadString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return ""
	}
}

func assertMigrationMarker(t *testing.T, db *upstream.Store, version int64, name string) {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != version || rows.Rows[0][1] != name {
		t.Fatalf("migration markers=%#v, want only %d/%q", rows.Rows, version, name)
	}
}

func assertColumns(t *testing.T, db *upstream.Store, table string, want []string) {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) != 1 {
			t.Fatalf("bad PRAGMA row for %s: %#v", table, row)
		}
		got[row[0].(string)] = true
	}
	for _, column := range want {
		if !got[column] {
			t.Fatalf("table %s missing column %s", table, column)
		}
	}
}

func assertObjects(t *testing.T, db *upstream.Store, kind string, want []string) {
	t.Helper()
	for _, name := range want {
		rows, err := db.Query(context.Background(), `SELECT name FROM sqlite_master WHERE type=? AND name=?`, kind, name)
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("missing %s %s: rows=%#v err=%v", kind, name, rows.Rows, err)
		}
	}
}

func assertAbsentObjects(t *testing.T, db *upstream.Store, names []string) {
	t.Helper()
	for _, name := range names {
		rows, err := db.Query(context.Background(), `SELECT name FROM sqlite_master WHERE name=?`, name)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows.Rows) != 0 {
			t.Fatalf("obsolete object %s exists", name)
		}
	}
}

func assertFinalSharedSchema(t *testing.T, db *upstream.Store) {
	t.Helper()
	for _, table := range sharedSchemaTables() {
		assertColumns(t, db, table.name, table.columns)
	}
	assertObjects(t, db, "index", []string{"hub_outbox_pending_idx", "hub_outbox_due_idx", "hub_outbox_operation_idx", "hub_outbox_retry_idx", "shared_operations_entity_idx", "shared_train_task_admissions_train_idx", "shared_entity_revisions_project_idx"})
	assertObjects(t, db, "trigger", []string{"shared_train_task_admission_conflict", "shared_train_task_admission_update_conflict"})
}

func assertFinalLocalSchema(t *testing.T, db *upstream.Store) {
	t.Helper()
	for _, table := range localSchemaPlan().tables {
		assertColumns(t, db, table.name, table.columns)
	}
	assertObjects(t, db, "index", []string{"local_events_kind_idx", "local_events_recorded_idx", "local_events_project_idx", "local_logs_filter_idx", "local_logs_recorded_idx", "local_logs_project_idx", "local_callback_epochs_pending_idx", "local_agents_project_idx", "local_sessions_updated_idx"})
}
