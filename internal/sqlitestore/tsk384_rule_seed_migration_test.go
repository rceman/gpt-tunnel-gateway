package sqlitestore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk384SeedFixtureConfig() model.ProjectConfiguration {
	now := time.Date(2026, 9, 18, 13, 39, 0, 0, time.UTC)
	configuration := model.DefaultProjectConfiguration("example", now)
	configuration.ProjectID = "example"
	configuration.Revision = 3
	return configuration
}

func tsk384WriteSeedProvenance(t *testing.T, db *Databases) {
	t.Helper()
	ctx := context.Background()
	configuration := tsk384SeedFixtureConfig()
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", SharedEntity{
		ID:        "example",
		Revision:  int64(configuration.Revision),
		Payload:   payload,
		UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT OR IGNORE INTO shared_project_identifiers(project_id,project_code) VALUES(?,?)`, "example", "EXM"); err != nil {
		t.Fatal(err)
	}
}

func tsk384SeededRules(t *testing.T, db *Databases) map[string]model.Rule {
	t.Helper()
	rows, err := db.Shared.Query(context.Background(), `SELECT payload FROM shared_rules ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	rules := make(map[string]model.Rule, len(rows.Rows))
	for _, row := range rows.Rows {
		raw, ok := row[0].([]byte)
		if !ok {
			t.Fatalf("unexpected rule payload column: %#v", row)
		}
		var rule model.Rule
		if err := json.Unmarshal(raw, &rule); err != nil {
			t.Fatal(err)
		}
		rules[rule.Name] = rule
	}
	return rules
}

func TestTSK384RuleSeedMigrationFailsClosedWithoutCanonicalPolicy(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 18, 13, 39, 0, 0, time.UTC))
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", SharedEntity{
		ID:        configuration.ProjectID,
		Revision:  int64(configuration.Revision),
		Payload:   payload,
		UpdatedAt: configuration.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM schema_migrations WHERE version=?`, sharedRuleSeedMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(state); err == nil {
		reopened.Close()
		t.Fatal("migration invented workflow policy defaults without canonical Rules")
	} else if !strings.Contains(err.Error(), "refusing to invent policy defaults") {
		t.Fatalf("unexpected migration failure: %v", err)
	}
}

func TestTSK384RuleSeedMigrationIsBoundedWithoutConfigOrIdentifiers(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Shared.Query(context.Background(), `SELECT COUNT(*) FROM shared_rules`)
	if err != nil {
		t.Fatal(err)
	}
	if count, _ := rows.Rows[0][0].(int64); count != 0 {
		t.Fatalf("fresh store seeded rules without provenance: %d", count)
	}
}

func TestTSK384RuleNameUniquenessIsEnforcedAtomically(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	payload := func(id string) []byte {
		raw, err := json.Marshal(map[string]any{"schema_version": 1, "id": id, "project_id": "example", "revision": 1, "title": "t", "summary": "s", "status": "accepted", "name": "ci.release", "value": "release"})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-RUL1", 1, payload("EXM-RUL1"), "2026-09-18T13:39:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-RUL2", 1, payload("EXM-RUL2"), "2026-09-18T13:39:00Z"); err == nil {
		t.Fatal("duplicate rule name accepted by the unique index")
	}
	// narrative rules share no name identity and never collide on NULL
	narrative, err := json.Marshal(map[string]any{"schema_version": 1, "id": "EXM-RUL2", "project_id": "example", "revision": 1, "title": "t", "summary": "s", "status": "proposed", "description": "body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_rules(id,revision,payload,updated_at) VALUES(?,?,?,?)`, "EXM-RUL2", 1, narrative, "2026-09-18T13:39:00Z"); err != nil {
		t.Fatalf("narrative rule rejected by name index: %v", err)
	}
}
