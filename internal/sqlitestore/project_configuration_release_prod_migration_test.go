package sqlitestore

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK529ReleaseProdInstallsOnCanonicalGTWConfiguration(t *testing.T) {
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
	if err := db.MigrateGTWReleaseProdProcedure(ctx); err != nil {
		t.Fatalf("release_prod migration failed: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("expected revision bump %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
	}
	definition, ok := migrated.Procedures[model.ReleaseProdProcedureName]
	if !ok {
		t.Fatalf("release_prod Procedure was not installed: %v", migrated.Procedures)
	}
	if definition.Script != "scripts/release-prod.py" {
		t.Fatalf("release_prod Procedure bound to %q", definition.Script)
	}
	if err := model.ValidateProjectProcedureDefinition(definition); err != nil {
		t.Fatalf("release_prod Procedure definition is invalid: %v", err)
	}
	// Idempotent replay.
	if err := db.MigrateGTWReleaseProdProcedure(ctx); err != nil {
		t.Fatalf("release_prod replay failed: %v", err)
	}
	if replayed := tsk627ReadGTWConfiguration(t, db); replayed.Revision != migrated.Revision {
		t.Fatal("release_prod replay bumped the configuration revision")
	}
}

func TestTSK529ReleaseProdRejectsConflictingProcedure(t *testing.T) {
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
	tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{model.ReleaseProdProcedureName: conflicting})
	if err := db.MigrateGTWReleaseProdProcedure(ctx); err == nil {
		t.Fatal("conflicting release_prod Procedure was silently overwritten")
	}
}
