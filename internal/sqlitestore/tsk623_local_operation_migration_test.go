package sqlitestore

import (
	"context"
	"errors"
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
		`CREATE UNIQUE INDEX local_operations_mutation_idx ON local_operations(mutation_id)`,
		`CREATE INDEX local_operations_project_idx ON local_operations(project_id,operation_number)`,
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
