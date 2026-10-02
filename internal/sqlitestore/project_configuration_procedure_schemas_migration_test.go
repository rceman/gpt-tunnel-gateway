package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// tsk687DriftedProcedure returns a copy of canonical whose schema fields are
// rewritten to the defective pre-TSK686 shape: fingerprint output fields as
// inline strings and cross-domain identity fields as plain strings. The
// Script/Summary/Guide identity stays canonical, so the definition is the
// same Procedure with a drifted schema.
func tsk687DriftedProcedure(t *testing.T, canonical model.ProjectProcedureDefinition) model.ProjectProcedureDefinition {
	t.Helper()
	inline := map[string]any{"type": "string", "minLength": 1, "maxLength": 256}
	drifted := canonical
	decode := func(schema map[string]any) map[string]any {
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		var clone map[string]any
		if err := json.Unmarshal(raw, &clone); err != nil {
			t.Fatal(err)
		}
		return clone
	}
	input := decode(canonical.Input)
	output := decode(canonical.Output)
	for _, direction := range []map[string]any{input, output} {
		for field := range direction["properties"].(map[string]any) {
			if field == "track" || field == "source_commit" || field == "source_tree" || field == "tag_object" {
				direction["properties"].(map[string]any)[field] = inline
			}
		}
	}
	drifted.Input = input
	drifted.Output = output
	return drifted
}

// TestTSK687ProcedureSchemasConvergeBehindCompletedMarkers reproduces the
// live failure: hosts installed the delivery Procedures with the defective
// schemas and completed the one-shot install markers, so the per-procedure
// reconcile can never run again. The dedicated schema migration must still
// converge all three definitions through one configuration revision, and the
// reconciled definitions must compile through the canonical contract path.
func TestTSK687ProcedureSchemasConvergeBehindCompletedMarkers(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	canonical, err := gtwDeliveryProcedureDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	installed := map[string]model.ProjectProcedureDefinition{
		"task_verify": {Script: "scripts/task-verify.py", Summary: "verify", Guide: "verify",
			Input:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			Output: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
	}
	for name, definition := range canonical {
		installed[name] = tsk687DriftedProcedure(t, definition)
	}
	original := tsk627SeedGTWConfiguration(t, db, installed)
	// The one-shot install markers are already complete — the per-procedure
	// reconcile branches must be unreachable for this host.
	for _, marker := range []string{
		"project_configuration_activation_preflight",
		"project_configuration_activate_local",
		"project_configuration_release_prod",
		// TSK693's e2e install is a separate one-shot marker; pre-complete it
		// so this fixture isolates the schema-convergence revision bump.
		"project_configuration_e2e_procedure_v1",
		"project_configuration_e2e_procedure_v2",
	} {
		if err := db.setSharedUpgradeMigrationState(ctx, marker, "complete"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.migrateGTWDeliveryProcedures(ctx); err != nil {
		t.Fatalf("delivery procedure chain failed to converge drifted schemas: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("schema convergence should commit one revision %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
	}
	contracts, err := actioncontract.LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	for name, definition := range canonical {
		wanted, err := json.Marshal(definition)
		if err != nil {
			t.Fatal(err)
		}
		current, err := json.Marshal(migrated.Procedures[name])
		if err != nil {
			t.Fatal(err)
		}
		if string(current) != string(wanted) {
			t.Fatalf("Procedure %s did not converge to the canonical schema", name)
		}
		if _, err := contracts.CompileProcedureAction("procedure/"+name, definition.Summary, definition.Guide, definition.Input, definition.Output); err != nil {
			t.Fatalf("reconciled procedure/%s fails the canonical compiler: %v", name, err)
		}
	}
	// Idempotent replay: markers complete and definitions canonical.
	if err := db.migrateGTWDeliveryProcedures(ctx); err != nil {
		t.Fatal(err)
	}
	if replayed := tsk627ReadGTWConfiguration(t, db); replayed.Revision != migrated.Revision {
		t.Fatal("schema convergence replay bumped the configuration revision")
	}
}

// TestTSK687ProcedureSchemasRejectsForeignProcedure keeps the fail-closed
// guarantee: a foreign Procedure under a canonical name is never rewritten.
func TestTSK687ProcedureSchemasRejectsForeignProcedure(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	foreign := model.ProjectProcedureDefinition{
		Script: "scripts/other.py", Summary: "other", Guide: "other",
		Input:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Output: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
	tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{model.ActivateLocalProcedureName: foreign})
	if err := db.MigrateGTWProcedureSchemas(ctx); err == nil {
		t.Fatal("foreign Procedure under a canonical name was silently rewritten")
	}
}
