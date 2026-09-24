package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectConfigurationHardCutMigratesCurrentOutboxAndHistory(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, projectConfigurationHardCutMigrationID); err != nil {
		t.Fatal(err)
	}
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = 1
	fields["execution_model"] = "train_v2"
	delete(fields, "guide_bindings")
	workflow := fields["workflow"].(map[string]any)
	delete(workflow, "gate_commands")
	integration := fields["integration"].(map[string]any)
	delete(integration, "target_branch")
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	now := configuration.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, configuration.ProjectID, configuration.Revision, legacy, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "config-migration-outbox", "project_configuration", configuration.ProjectID, configuration.ProjectID, configuration.Revision, "project-configuration-update", legacy, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", configuration.ProjectID, configuration.ProjectID, configuration.Revision, "update", "planner", "initial", []byte(`[]`), legacy, now); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		name string
		sql  string
		args []any
	}{
		{name: "current", sql: `SELECT payload FROM shared_project_configurations WHERE id=?`, args: []any{configuration.ProjectID}},
		{name: "outbox", sql: `SELECT payload FROM hub_outbox WHERE id=?`, args: []any{"config-migration-outbox"}},
		{name: "history", sql: `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=?`, args: []any{configuration.ProjectID, configuration.Revision}},
	} {
		rows, err := db.Shared.Query(ctx, query.sql, query.args...)
		if err != nil || len(rows.Rows) != 1 || len(rows.Rows[0]) != 1 {
			t.Fatalf("%s payload query rows=%#v err=%v", query.name, rows, err)
		}
		payload, ok := rows.Rows[0][0].([]byte)
		if !ok {
			t.Fatalf("%s payload has type %T", query.name, rows.Rows[0][0])
		}
		got, err := DecodeCanonicalProjectConfigurationPayload(payload)
		if err != nil || got.SchemaVersion != model.ProjectConfigurationSchemaVersion || bytes.Contains(payload, []byte(`"execution_model"`)) {
			t.Fatalf("%s payload was not canonical: %#v err=%v", query.name, got, err)
		}
		if got.GuideBindings == nil || got.Workflow.GateCommands.IsZero() || got.Integration.TargetBranch != got.Workflow.IntegrationBranch {
			t.Fatalf("%s payload missed migrated defaults: %#v", query.name, got)
		}
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
}

func TestProjectConfigurationHardCutRejectsUnknownRetiredForms(t *testing.T) {
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["execution_model"] = "future_mode"
	invalid, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := MigrateProjectConfigurationPayload(invalid); err == nil {
		t.Fatal("unsupported execution model was accepted")
	}
	if _, err := DecodeCanonicalProjectConfigurationPayload(invalid); err == nil {
		t.Fatal("canonical decoder accepted a retired execution model")
	}
}
