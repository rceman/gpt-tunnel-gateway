package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// TestTSK695E2EOutputForeignTrackKeyCompletes reproduces GTW-OPR4017: the e2e
// receipt names a Track that only exists on the target runtime, so the output
// schema must carry it as an opaque bounded string. If it were an
// EntityKeyAndReference the executor would try to resolve it against the
// owning project and the Operation would land outcome_unknown.
func TestTSK695E2EOutputForeignTrackKeyCompletes(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	// Install a stub under the canonical script path; the stub emits the real
	// receipt shape with a Track key that does not exist on this project.
	if err := os.MkdirAll(filepath.Join(root, "procedures"), 0o700); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
python3 - "$GTW_PROCEDURE_OUTPUT_FILE" <<'PYEOF'
import json, sys
receipt = {"op": "track_accept", "target_project": "CLN", "target_track": "CLN-TRK1", "revision": 6, "head": "0f420de1", "tree": "0d8e45b3", "status": "accepted"}
with open(sys.argv[1], "w") as handle:
    json.dump(receipt, handle)
PYEOF
`
	if err := os.WriteFile(filepath.Join(root, model.GTWE2EScriptPath), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	definition, err := model.GTWE2EProcedureDefinition()
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       model.GTWE2EProcedureName,
		Definition: definition,
		Reason:     "Install e2e for output-reference regression coverage.",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Admission compares the stored definition verbatim; pass the durable
	// read-back so JSON-normalized values match.
	stored, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	storedDefinition, exists := stored.Procedures[model.GTWE2EProcedureName]
	if !exists {
		t.Fatal("e2e Procedure was not stored")
	}
	receipt, err := s.ProcedureExecutionStart(ctx, ProcedureExecutionStartInput{
		ProjectID:             "example",
		Name:                  model.GTWE2EProcedureName,
		Definition:            storedDefinition,
		ConfigurationRevision: created.Revision,
		Input:                 json.RawMessage(`{"op":"track_accept","listen_addr":"127.0.0.1:9","target_project":"CLN","planner_session":"HOM_CLN_P_x","target_track":"CLN-TRK1","track_revision":5,"head":"0f420de1","tree":"0d8e45b3","submitted_by":"HOM_CLN_L_x","submitted_at":"26-01-01T00:00:00","tasks":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.Operation)
	if operation.Status != "completed" {
		t.Fatalf("e2e Procedure Operation did not complete with the foreign Track key: %#v", operation)
	}
	var output map[string]any
	if err := json.Unmarshal(operation.Result, &output); err != nil {
		t.Fatalf("e2e receipt is not structured output: %v", err)
	}
	if output["target_track"] != "CLN-TRK1" || output["status"] != "accepted" {
		t.Fatalf("e2e receipt does not carry the foreign Track key: %#v", output)
	}
}

// TestTSK695EntityReferenceOutputFailsClosed proves the regression leg binds
// the fix: had the receipt still declared track as EntityKeyAndReference the
// executor would resolve it against the owning project and fail closed with
// outcome_unknown — exactly the GTW-OPR4017 shape.
func TestTSK695EntityReferenceOutputFailsClosed(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	script := procedureTestScript(t, root, `#!/bin/sh
printf '{"track":"CLN-TRK1","status":"accepted"}' > "$GTW_PROCEDURE_OUTPUT_FILE"
`)
	definition := model.ProjectProcedureDefinition{
		Script:  script,
		Summary: "Emit a foreign Track reference.",
		Guide:   "Output declares track as an entity reference the owning project cannot resolve.",
		Input:   map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Output: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"track": map[string]any{"$ref": "EntityKeyAndReference"}, "status": map[string]any{"type": "string"}},
			"required":             []any{"track", "status"},
			"additionalProperties": false,
		},
	}
	created, err := s.ConfigProcedureCreate(ctx, ConfigProcedureCreateInput{
		ProjectID:  "example",
		Name:       "foreign_track",
		Definition: definition,
		Reason:     "Regression fixture.",
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.ProcedureExecutionStart(ctx, ProcedureExecutionStartInput{
		ProjectID:             "example",
		Name:                  "foreign_track",
		Definition:            definition,
		ConfigurationRevision: created.Revision,
		Input:                 json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.Operation)
	if operation.Status != "outcome_unknown" {
		t.Fatalf("foreign EntityKeyAndReference output must land outcome_unknown, got %#v", operation)
	}
}
