package sqlitestore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK666Gate20SharedConfigurationOutboxRevisionMigration(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationHardCutMigrationID, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_upgrade_migrations WHERE migration_id=?`, projectConfigurationRetiredFieldMigrationID); err != nil {
		t.Fatal(err)
	}
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationRetiredFieldMigrationID, "in_progress"); err != nil {
		t.Fatal(err)
	}
	configuration := model.DefaultProjectConfiguration("example", time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	currentRetired := projectConfigurationRetiredFieldsFixture(t, data)
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = 1
	fields["execution_model"] = "train_v2"
	delete(fields, "guide_bindings")
	integration := fields["integration"].(map[string]any)
	delete(integration, "target_branch")
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	legacy = projectConfigurationRetiredFieldsFixture(t, legacy)
	secondConfiguration := configuration
	secondConfiguration.Revision++
	secondConfiguration.UpdatedAt = configuration.UpdatedAt.Add(time.Minute)
	secondData, err := json.Marshal(secondConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	secondRetired := projectConfigurationRetiredFieldsFixture(t, secondData)
	now := configuration.UpdatedAt.Format(time.RFC3339Nano)
	secondNow := secondConfiguration.UpdatedAt.Format(time.RFC3339Nano)
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, configuration.ProjectID, configuration.Revision, currentRetired, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "config-migration-outbox", "project_configuration", configuration.ProjectID, configuration.ProjectID, configuration.Revision, "project-configuration-update", legacy, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO hub_outbox(id,entity_type,entity_id,project_id,revision,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?)`, "config-migration-outbox-2", "project_configuration", secondConfiguration.ProjectID, secondConfiguration.ProjectID, secondConfiguration.Revision, "project-configuration-update", secondRetired, secondNow); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", configuration.ProjectID, configuration.ProjectID, configuration.Revision, "update", "planner", "initial", []byte(`[]`), currentRetired, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_revisions(entity_type,entity_id,project_id,revision,mutation_kind,actor,reason,changed_fields,payload,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "project_configuration", secondConfiguration.ProjectID, secondConfiguration.ProjectID, secondConfiguration.Revision, "update", "planner", "follow-up", []byte(`[]`), secondRetired, secondNow); err != nil {
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
		{name: "outbox-v1", sql: `SELECT payload FROM hub_outbox WHERE id=?`, args: []any{"config-migration-outbox"}},
		{name: "outbox-v2", sql: `SELECT payload FROM hub_outbox WHERE id=?`, args: []any{"config-migration-outbox-2"}},
		{name: "history-v1", sql: `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=?`, args: []any{configuration.ProjectID, configuration.Revision}},
		{name: "history-v2", sql: `SELECT payload FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id=? AND revision=?`, args: []any{secondConfiguration.ProjectID, secondConfiguration.Revision}},
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
		if err != nil || got.SchemaVersion != model.ProjectConfigurationSchemaVersion || bytes.Contains(payload, []byte(`"execution_model"`)) || bytes.Contains(payload, []byte(`"watcher"`)) || bytes.Contains(payload, []byte(`"train"`)) {
			t.Fatalf("%s payload was not canonical: %#v err=%v", query.name, got, err)
		}
		if got.GuideBindings == nil || got.Workflow.GateCommands.IsZero() || got.Integration.TargetBranch != got.Workflow.IntegrationBranch {
			t.Fatalf("%s payload missed migrated defaults: %#v", query.name, got)
		}
	}
	state, err := db.sharedUpgradeMigrationState(ctx, projectConfigurationRetiredFieldMigrationID)
	if err != nil || state != "complete" {
		t.Fatalf("retired-field migration state=%q err=%v", state, err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
}

func projectConfigurationRetiredFieldsFixture(t *testing.T, data []byte) []byte {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["watcher"] = map[string]any{"enabled": true, "interval_seconds": 15}
	workflow := fields["workflow"].(map[string]any)
	gateCommands := workflow["gate_commands"].(map[string]any)
	testGate := gateCommands["test"].(map[string]any)
	testGate["train"] = map[string]any{"command": []string{"go", "test", "./..."}}
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return payload
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
