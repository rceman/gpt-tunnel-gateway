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

// tsk695DriftedE2EDefinition returns the canonical identity fields with the
// pre-TSK695 output schema: the Track named as an owning-project
// EntityKeyAndReference — the exact shape live hosts installed under the
// completed v1 marker.
func tsk695DriftedE2EDefinition(t *testing.T) model.ProjectProcedureDefinition {
	t.Helper()
	canonical, err := model.GTWE2EProcedureDefinition()
	if err != nil {
		t.Fatal(err)
	}
	output, _ := canonical.Output["properties"].(map[string]any)
	output["track"] = map[string]any{"$ref": "EntityKeyAndReference"}
	delete(output, "target_track")
	required, _ := canonical.Output["required"].([]any)
	for index, value := range required {
		if value == "target_track" {
			required[index] = "track"
		}
	}
	return canonical
}

// TestTSK695E2EProcedureV2ConvergesDriftedDefinition reproduces the live host
// state: the v1 marker is complete but the stored definition still carries
// the defective output schema. The v2 migration must rewrite it to canonical
// under one configuration revision and enqueue the Hub publication.
func TestTSK695E2EProcedureV2ConvergesDriftedDefinition(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	original := tsk627SeedGTWConfiguration(t, db, map[string]model.ProjectProcedureDefinition{
		model.GTWE2EProcedureName: tsk695DriftedE2EDefinition(t),
	})
	if err := db.setSharedUpgradeMigrationState(ctx, projectConfigurationE2EMigrationID, "complete"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateGTWE2EProcedureV2(ctx); err != nil {
		t.Fatalf("e2e v2 convergence failed: %v", err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("v2 convergence should commit one revision %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
	}
	canonical, err := model.GTWE2EProcedureDefinition()
	if err != nil {
		t.Fatal(err)
	}
	wanted, _ := json.Marshal(canonical)
	current, _ := json.Marshal(migrated.Procedures[model.GTWE2EProcedureName])
	if string(current) != string(wanted) {
		t.Fatalf("v2 did not converge the e2e definition:\nwant %s\ngot  %s", wanted, current)
	}
	outbox, err := db.Shared.Query(ctx, `SELECT id FROM hub_outbox WHERE entity_type='project_configuration'`)
	if err != nil || len(outbox.Rows) != 1 {
		t.Fatalf("v2 convergence must enqueue exactly one publication: %v", outbox.Rows)
	}
	// Replay is idempotent: marker complete, no second write.
	if err := db.MigrateGTWE2EProcedureV2(ctx); err != nil {
		t.Fatal(err)
	}
	if replayed := tsk627ReadGTWConfiguration(t, db); replayed.Revision != migrated.Revision {
		t.Fatal("v2 replay bumped the configuration revision")
	}
}

// TestTSK695E2EProcedureV2RejectsForeignDefinition keeps the fail-closed
// guarantee under the new marker: a foreign Procedure under the e2e name is
// never rewritten.
func TestTSK695E2EProcedureV2RejectsForeignDefinition(t *testing.T) {
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
	if err := db.MigrateGTWE2EProcedureV2(ctx); err == nil {
		t.Fatal("foreign Procedure under the e2e name was silently rewritten by v2")
	}
}

// TestTSK695E2EProcedureV2FreshInstall covers hosts where the v1 marker never
// ran: the chain installs canonical via v1 and v2 converges without a second
// revision.
func TestTSK695E2EProcedureV2FreshInstall(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	original := tsk627SeedGTWConfiguration(t, db, nil)
	if err := db.MigrateGTWE2EProcedure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateGTWE2EProcedureV2(ctx); err != nil {
		t.Fatal(err)
	}
	migrated := tsk627ReadGTWConfiguration(t, db)
	if migrated.Revision != original.Revision+1 {
		t.Fatalf("fresh install should commit one revision %d -> %d, got %d", original.Revision, original.Revision+1, migrated.Revision)
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
