package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
)

func TestTSK623ExistingLocalOperationsUpgradeBeforeAdmissionQuery(t *testing.T) {
	stateDir := t.TempDir()
	installTSK623PreviousLocalSchema(t, stateDir)

	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	legacy, err := db.ReadLocalOperation(ctx, "EXM-OPR1")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ProjectID != "example" || legacy.ProjectCode != "EXM" || legacy.OperationNumber != 1 || legacy.MutationID != strings.Repeat("a", 64) || legacy.Kind != "agent-prompt" || legacy.Status != "completed" || string(legacy.ResultPayload) != `{"legacy":true}` || legacy.Error != "legacy-error" || legacy.RecoveryReason != "legacy-recovery" {
		t.Fatalf("legacy operation changed during upgrade=%#v", legacy)
	}
	if legacy.AdmissionSessionID != "" || legacy.AdmissionInputSHA256 != "" {
		t.Fatalf("legacy operation received non-empty admission coordinate=%#v", legacy)
	}

	admitted, err := db.AllocateLocalOperationWithAdmissionCoordinate(ctx, "example", "EXM", strings.Repeat("b", 64), "agent-prompt", "HOM_EXM_P_legacy", strings.Repeat("c", 64), time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("post-upgrade admission failed: %v", err)
	}
	if admitted.OperationID != "EXM-OPR2" || admitted.AdmissionSessionID != "HOM_EXM_P_legacy" || admitted.AdmissionInputSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("post-upgrade operation=%#v", admitted)
	}
	matched, err := db.ListLocalOperationsByAdmissionCoordinate(ctx, "example", "agent-prompt", admitted.AdmissionSessionID, admitted.AdmissionInputSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].OperationID != admitted.OperationID {
		t.Fatalf("admission lookup=%#v", matched)
	}

	assertColumns(t, db.Local, "local_operations", localSchemaPlan().tables[1].columns)
	assertObjects(t, db.Local, "index", []string{"local_operations_admission_idx"})
	markers, err := db.Local.Query(ctx, `SELECT version,name FROM schema_migrations WHERE version=?`, localOperationAdmissionMigrationVersion)
	if err != nil || len(markers.Rows) != 1 || markers.Rows[0][1] != localOperationAdmissionMigrationName {
		t.Fatalf("admission migration marker=%#v err=%v", markers.Rows, err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.ReadLocalOperation(ctx, admitted.OperationID); err != nil {
		t.Fatalf("reopened upgraded operation unavailable: %v", err)
	}
}

func TestTSK623FreshAndUpgradedLocalSchemasConverge(t *testing.T) {
	fresh, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	freshSchema := tsk623LocalSchemaSnapshot(t, fresh.Local)

	upgradedState := t.TempDir()
	installTSK623PreviousLocalSchema(t, upgradedState)
	upgraded, err := Open(upgradedState)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	upgradedSchema := tsk623LocalSchemaSnapshot(t, upgraded.Local)
	if !reflect.DeepEqual(upgradedSchema, freshSchema) {
		t.Fatalf("fresh and upgraded Local schemas diverged:\nfresh=%#v\nupgraded=%#v", freshSchema, upgradedSchema)
	}
}

func tsk623LocalSchemaSnapshot(t *testing.T, db *upstream.Store) map[string]string {
	t.Helper()
	ctx := context.Background()
	snapshot := map[string]string{}
	tables, err := db.Query(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range tables.Rows {
		if len(row) != 1 {
			t.Fatalf("invalid Local table row=%#v", row)
		}
		name, ok := row[0].(string)
		if !ok {
			t.Fatalf("invalid Local table name=%#v", row)
		}
		columns, queryErr := db.Query(ctx, `SELECT name,type,"notnull",dflt_value,pk FROM pragma_table_info(?) ORDER BY cid`, name)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		snapshot["table:"+name] = fmt.Sprintf("%#v", columns.Rows)
	}
	indexes, err := db.Query(ctx, `SELECT name,sql FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_autoindex_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range indexes.Rows {
		if len(row) != 2 {
			t.Fatalf("invalid Local index row=%#v", row)
		}
		name, nameOK := row[0].(string)
		sql, sqlOK := row[1].(string)
		if !nameOK || !sqlOK {
			t.Fatalf("invalid Local index=%#v", row)
		}
		snapshot["index:"+name] = strings.ReplaceAll(sql, " IF NOT EXISTS", "")
	}
	markers, err := db.Query(ctx, `SELECT version,name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot["migration_markers"] = fmt.Sprintf("%#v", markers.Rows)
	return snapshot
}

func TestTSK623MigrationFailureCannotReportLocalStoreReady(t *testing.T) {
	stateDir := t.TempDir()
	_, localPath := Paths(stateDir)
	raw, err := upstream.Open(engineConfig(Config{})(localPath))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := raw.Exec(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	for _, marker := range [][2]any{{localBaselineVersion, localBaselineName}, {localTokenUsageMigrationVersion, localTokenUsageMigrationName}, {localOperationMigrationVersion, localOperationMigrationName}} {
		if _, err := raw.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, marker[0], marker[1]); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(stateDir)
	if err == nil {
		t.Fatal("incomplete Local schema reported ready")
	}
	var openErr *OpenError
	if !errors.As(err, &openErr) || openErr.Stage != "migration" || openErr.Database != "local" {
		t.Fatalf("migration failure=%T %v", err, err)
	}
}

func installTSK623PreviousLocalSchema(t *testing.T, stateDir string) {
	t.Helper()
	_, localPath := Paths(stateDir)
	raw, err := upstream.Open(engineConfig(Config{})(localPath))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE local_operation_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_number INTEGER NOT NULL CHECK(next_number BETWEEN 1 AND 9007199254740991))`,
		`CREATE TABLE local_operations (operation_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, project_code TEXT NOT NULL, operation_number INTEGER NOT NULL, mutation_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, status TEXT NOT NULL, result_payload BLOB, error TEXT NOT NULL DEFAULT '', recovery_reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, project_id TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`,
		`CREATE TABLE local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, project_id TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`,
		`CREATE TABLE local_callback_epochs (epoch_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, agent_id TEXT NOT NULL DEFAULT '', session_key TEXT NOT NULL, armed_at TEXT NOT NULL, busy_seen INTEGER NOT NULL DEFAULT 0, idle_observations INTEGER NOT NULL DEFAULT 0, emitted_at TEXT)`,
		`CREATE TABLE local_agents (project_id TEXT NOT NULL, agent_id TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(project_id,agent_id))`,
		`CREATE TABLE local_sessions (session_id TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','ended')))`,
		`CREATE TABLE local_token_usage_events (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0), output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0), total_tokens INTEGER NOT NULL CHECK(total_tokens >= 0), recorded_at TEXT NOT NULL)`,
		`CREATE TABLE local_token_usage (session_id TEXT PRIMARY KEY, request_count INTEGER NOT NULL CHECK(request_count >= 0), input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0), output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0), total_tokens INTEGER NOT NULL CHECK(total_tokens >= 0), updated_at TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX local_operations_mutation_idx ON local_operations(mutation_id)`,
		`CREATE INDEX local_operations_project_idx ON local_operations(project_id,operation_number)`,
		`CREATE INDEX local_operation_sequences_project_idx ON local_operation_sequences(project_id)`,
		`CREATE INDEX local_events_kind_idx ON local_events(kind,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_events_recorded_idx ON local_events(recorded_at DESC,id DESC)`,
		`CREATE INDEX local_events_project_idx ON local_events(project_id,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_logs_filter_idx ON local_logs(level,component,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_logs_recorded_idx ON local_logs(recorded_at DESC,id DESC)`,
		`CREATE INDEX local_logs_project_idx ON local_logs(project_id,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_callback_epochs_pending_idx ON local_callback_epochs(emitted_at,armed_at,epoch_id)`,
		`CREATE INDEX local_agents_project_idx ON local_agents(project_id,agent_id)`,
		`CREATE INDEX local_sessions_updated_idx ON local_sessions(updated_at,session_id)`,
		`CREATE UNIQUE INDEX local_token_usage_events_session_idx ON local_token_usage_events(session_id,event_id)`,
	}
	for _, statement := range statements {
		if _, err := raw.Exec(ctx, statement); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	for _, marker := range [][2]any{{localBaselineVersion, localBaselineName}, {localTokenUsageMigrationVersion, localTokenUsageMigrationName}, {localOperationMigrationVersion, localOperationMigrationName}} {
		if _, err := raw.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, marker[0], marker[1]); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	created := "2026-09-16T09:00:00Z"
	if _, err := raw.Exec(ctx, `INSERT INTO local_operation_sequences(project_id,project_code,next_number) VALUES(?,?,?)`, "example", "EXM", 2); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(ctx, `INSERT INTO local_operations(operation_id,project_id,project_code,operation_number,mutation_id,kind,status,result_payload,error,recovery_reason,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "EXM-OPR1", "example", "EXM", 1, strings.Repeat("a", 64), "agent-prompt", "completed", []byte(`{"legacy":true}`), "legacy-error", "legacy-recovery", created, created); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}
