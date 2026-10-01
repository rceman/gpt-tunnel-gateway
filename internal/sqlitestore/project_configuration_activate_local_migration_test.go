package sqlitestore

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK606ActivateLocalInstallsOnCanonicalGTWConfiguration(t *testing.T) {
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
	if err := db.MigrateGTWActivateLocalProcedure(ctx); err != nil {
		t.Fatalf("activate_local migration failed: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("expected revision bump %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
	}
	definition, ok := migrated.Procedures[model.ActivateLocalProcedureName]
	if !ok {
		t.Fatalf("activate_local Procedure was not installed: %v", migrated.Procedures)
	}
	if definition.Script != "scripts/activate-local.py" {
		t.Fatalf("activate_local Procedure bound to %q", definition.Script)
	}
	if err := model.ValidateProjectProcedureDefinition(definition); err != nil {
		t.Fatalf("activate_local Procedure definition is invalid: %v", err)
	}
	if _, ok := migrated.Procedures["task_verify"]; !ok {
		t.Fatal("activate_local migration clobbered an existing Procedure")
	}
	// Idempotent replay.
	if err := db.MigrateGTWActivateLocalProcedure(ctx); err != nil {
		t.Fatalf("activate_local replay failed: %v", err)
	}
	if replayed := tsk627ReadGTWConfiguration(t, db); replayed.Revision != migrated.Revision {
		t.Fatal("activate_local replay bumped the configuration revision")
	}
}

func TestTSK606ActivateLocalRejectsConflictingProcedure(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conflicting := model.ProjectProcedureDefinition{
		Script: "scripts/other.py", Summary: "other", Guide: "other",
		Input:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Output: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
	tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{model.ActivateLocalProcedureName: conflicting})
	if err := db.MigrateGTWActivateLocalProcedure(ctx); err == nil {
		t.Fatal("conflicting activate_local Procedure was silently overwritten")
	}
}

func TestTSK606ActivateLocalChainRunsAfterPreflight(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tsk627SeedGTWConfiguration(t, db, nil)
	if err := db.migrateGTWActivationProcedures(ctx); err != nil {
		t.Fatalf("activation procedure chain failed: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	for _, name := range []string{model.ActivationPreflightProcedureName, model.ActivateLocalProcedureName} {
		if _, ok := migrated.Procedures[name]; !ok {
			t.Fatalf("Procedure %s was not installed: %v", name, migrated.Procedures)
		}
	}
}
