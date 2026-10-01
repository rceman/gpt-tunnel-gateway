package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk627SeedGTWConfiguration(t *testing.T, db *Databases, procedures map[string]model.ProjectProcedureDefinition) model.ProjectConfiguration {
	t.Helper()
	ctx := context.Background()
	configuration := model.DefaultProjectConfiguration("gpt-tunnel-gateway", time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	configuration.Revision = 7
	if procedures != nil {
		configuration.Procedures = procedures
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, configuration.ProjectID, configuration.Revision, payload, configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return configuration
}

func tsk627ReadGTWConfiguration(t *testing.T, db *Databases) model.ProjectConfiguration {
	t.Helper()
	rows, err := db.Shared.Query(context.Background(), `SELECT revision,payload FROM shared_project_configurations WHERE id='gpt-tunnel-gateway'`)
	if err != nil || len(rows.Rows) != 1 {
		t.Fatalf("read GTW configuration: err=%v rows=%v", err, rows.Rows)
	}
	revision, ok := rows.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("invalid GTW configuration revision %v", rows.Rows[0][0])
	}
	payload, ok := rows.Rows[0][1].([]byte)
	if !ok {
		t.Fatalf("invalid GTW configuration payload")
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(payload)
	if err != nil {
		t.Fatalf("decode migrated GTW configuration: %v", err)
	}
	if int64(configuration.Revision) != revision {
		t.Fatalf("payload revision %d does not match row revision %d", configuration.Revision, revision)
	}
	return configuration
}

func TestTSK627ActivationPreflightInstallsOnCanonicalGTWConfiguration(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gate := model.ProjectProcedureDefinition{
		Script: "scripts/task-verify.py", Summary: "verify", Guide: "verify",
		Input:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Output: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
	original := tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{"task_verify": gate})
	if err := db.MigrateGTWActivationPreflightProcedure(ctx); err != nil {
		t.Fatalf("activation_preflight migration failed: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("expected revision bump %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
	}
	definition, ok := migrated.Procedures[model.ActivationPreflightProcedureName]
	if !ok {
		t.Fatalf("activation_preflight Procedure was not installed: %v", migrated.Procedures)
	}
	if definition.Script != "scripts/activation-preflight.py" {
		t.Fatalf("activation_preflight Procedure bound to %q", definition.Script)
	}
	if migrated.Procedures["task_verify"].Script != gate.Script {
		t.Fatalf("existing task_verify Procedure was disturbed")
	}
	if err := model.ValidateProjectConfiguration(migrated); err != nil {
		t.Fatalf("migrated configuration is not valid: %v", err)
	}
	// The install must be a real durable lifecycle revision so Hub converges
	// through the normal outbox publish path.
	outbox, err := db.Shared.Query(ctx, `SELECT revision,kind,payload,published_at FROM hub_outbox WHERE entity_type='project_configuration' AND entity_id='gpt-tunnel-gateway'`)
	if err != nil || len(outbox.Rows) != 1 {
		t.Fatalf("expected one pending outbox entry for the Procedure install, got %v err=%v", outbox.Rows, err)
	}
	if outbox.Rows[0][0].(int64) != int64(migrated.Revision) || outbox.Rows[0][3] != nil {
		t.Fatalf("outbox entry does not carry the new revision: %v", outbox.Rows[0])
	}
	history, err := db.Shared.Query(ctx, `SELECT revision,mutation_kind,actor FROM shared_entity_revisions WHERE entity_type='project_configuration' AND entity_id='gpt-tunnel-gateway'`)
	if err != nil || len(history.Rows) != 1 {
		t.Fatalf("expected one Shared revision history row for the Procedure install, got %v err=%v", history.Rows, err)
	}
	state, err := db.projectConfigurationMigrationState(ctx, projectConfigurationActivationPreflightMigrationID)
	if err != nil || state != "complete" {
		t.Fatalf("migration marker state=%q err=%v", state, err)
	}
	complete, err := db.ProjectConfigurationMigrationComplete(ctx)
	if err != nil || !complete {
		t.Fatalf("ProjectConfigurationMigrationComplete=%v err=%v", complete, err)
	}
}

func TestTSK627ActivationPreflightMigrationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tsk627SeedGTWConfiguration(t, db, nil)
	if err := db.MigrateGTWActivationPreflightProcedure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateGTWActivateLocalProcedure(ctx); err != nil {
		t.Fatal(err)
	}
	first := tsk627ReadGTWConfiguration(t, db)
	if err := db.MigrateGTWActivationPreflightProcedure(ctx); err != nil {
		t.Fatalf("idempotent re-run failed: %v", err)
	}
	if err := db.MigrateProjectConfigurationToCanonical(ctx); err != nil {
		t.Fatalf("canonical re-run failed: %v", err)
	}
	second := tsk627ReadGTWConfiguration(t, db)
	if second.Revision != first.Revision {
		t.Fatalf("idempotent re-run changed revision %d -> %d", first.Revision, second.Revision)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT id FROM hub_outbox WHERE entity_type='project_configuration'`)
	if err != nil || len(outbox.Rows) != 2 {
		t.Fatalf("idempotent re-run enqueued extra publishes: %v", outbox.Rows)
	}
}

func TestTSK627ActivationPreflightConflictFailsClosed(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	foreign := model.ProjectProcedureDefinition{
		Script: "scripts/other.py", Summary: "conflict", Guide: "conflict",
		Input:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Output: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
	seeded := tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{
		model.ActivationPreflightProcedureName: foreign,
	})
	if err := db.MigrateGTWActivationPreflightProcedure(ctx); err == nil {
		t.Fatal("conflicting activation_preflight definition did not fail closed")
	}
	current := tsk627ReadGTWConfiguration(t, db)
	if current.Revision != seeded.Revision {
		t.Fatalf("conflict mutated the configuration revision")
	}
}

func TestTSK627ActivationPreflightAbsentGTWConfigurationIsNotComplete(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A non-GTW project alone must not complete the marker.
	other := model.DefaultProjectConfiguration("example", time.Now().UTC())
	payload, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_project_configurations(id,revision,payload,updated_at) VALUES(?,?,?,?)`, other.ProjectID, other.Revision, payload, other.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateGTWActivationPreflightProcedure(ctx); err != nil {
		t.Fatalf("non-GTW host migration failed: %v", err)
	}
	if _, exists := tsk627ReadGTWConfigurationAbsent(db); exists {
		t.Fatal("migration invented a GTW configuration row")
	}
	otherRows, err := db.Shared.Query(ctx, `SELECT payload FROM shared_project_configurations WHERE id='example'`)
	if err != nil || len(otherRows.Rows) != 1 {
		t.Fatal(err)
	}
	decoded, err := DecodeCanonicalProjectConfigurationPayload(otherRows.Rows[0][0].([]byte))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded.Procedures[model.ActivationPreflightProcedureName]; exists {
		t.Fatal("activation_preflight installed on a non-GTW project")
	}
}

func tsk627ReadGTWConfigurationAbsent(db *Databases) (model.ProjectConfiguration, bool) {
	rows, err := db.Shared.Query(context.Background(), `SELECT payload FROM shared_project_configurations WHERE id='gpt-tunnel-gateway'`)
	if err != nil || len(rows.Rows) == 0 {
		return model.ProjectConfiguration{}, false
	}
	configuration, err := DecodeCanonicalProjectConfigurationPayload(rows.Rows[0][0].([]byte))
	if err != nil {
		return model.ProjectConfiguration{}, false
	}
	return configuration, true
}

func TestTSK627ActivationPreflightMigrationCompleteSemantics(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// No GTW configuration: overall completeness holds so non-self-host
	// Gateways are not deferred forever.
	complete, err := db.ProjectConfigurationMigrationComplete(ctx)
	if err != nil || !complete {
		t.Fatalf("non-GTW host should be complete, got %v err=%v", complete, err)
	}
	tsk627SeedGTWConfiguration(t, db, nil)
	complete, err = db.ProjectConfigurationMigrationComplete(ctx)
	if err != nil || complete {
		t.Fatalf("unmigrated GTW host must report incomplete, got %v", complete)
	}
	if err := db.MigrateGTWActivationPreflightProcedure(ctx); err != nil {
		t.Fatal(err)
	}
	complete, err = db.ProjectConfigurationMigrationComplete(ctx)
	if err != nil || !complete {
		t.Fatalf("migrated GTW host must report complete, got %v", complete)
	}
}
