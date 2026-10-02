package sqlitestore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TestTSK693E2EProcedureInstallsAndCompiles proves the marker migration
// installs the canonical e2e Procedure into the GTW self-host configuration,
// the installed definition compiles through the canonical contract compiler
// (the exact procedure/<name> registration path), and replay is idempotent.
func TestTSK693E2EProcedureInstallsAndCompiles(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	definition, err := model.GTWE2EProcedureDefinition()
	if err != nil {
		t.Fatal(err)
	}
	original := tsk627SeedGTWConfiguration(t, db, nil)
	if err := db.MigrateGTWE2EProcedure(ctx); err != nil {
		t.Fatalf("e2e Procedure install failed: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("install should commit one revision %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
	}
	installed, exists := migrated.Procedures[model.GTWE2EProcedureName]
	if !exists {
		t.Fatal("e2e Procedure was not installed")
	}
	wanted, _ := json.Marshal(definition)
	current, _ := json.Marshal(installed)
	if string(current) != string(wanted) {
		t.Fatalf("installed e2e Procedure differs from canonical:\nwant %s\ngot  %s", wanted, current)
	}
	contracts, err := actioncontract.LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contracts.CompileProcedureAction("procedure/"+model.GTWE2EProcedureName, installed.Summary, installed.Guide, installed.Input, installed.Output); err != nil {
		t.Fatalf("installed e2e Procedure fails the canonical compiler: %v", err)
	}
	if err := db.MigrateGTWE2EProcedure(ctx); err != nil {
		t.Fatal(err)
	}
	if replayed := tsk627ReadGTWConfiguration(t, db); replayed.Revision != migrated.Revision {
		t.Fatal("e2e Procedure migration replay bumped the configuration revision")
	}
}

// TestTSK693E2EProcedureRejectsForeignDefinition keeps the fail-closed
// guarantee: a foreign Procedure under the e2e name is never rewritten.
func TestTSK693E2EProcedureRejectsForeignDefinition(t *testing.T) {
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
	tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{model.GTWE2EProcedureName: foreign})
	if err := db.MigrateGTWE2EProcedure(ctx); err == nil {
		t.Fatal("foreign Procedure under the e2e name was silently rewritten")
	}
}
