package model

import (
	"fmt"
)

// GTWE2EProcedureName is the canonical GTW-owned cross-project Procedure used
// by the MIL1 clean-room proof (TSK693, JRN25). The Planner invokes it with an
// approved exact Track review snapshot for a bounded loopback target runtime;
// the script verifies the target Planner Session and snapshot, performs
// track/accept through that Session only, and returns a typed receipt.
const GTWE2EProcedureName = "e2e"
const GTWE2EScriptPath = "procedures/e2e.py"

// GTWE2EProcedureInputSchema is the closed typed input contract. Only the
// track_accept operation exists; every field is a caller-supplied fact that
// the script must prove against live target state before mutation.
//
// Target-side facts deliberately avoid the canonical cross-domain field names
// (track, project, session): those contract names compile to
// EntityKeyAndReference, which resolves against the Procedure-owning project
// only — and the whole point of this Procedure is that its target is a
// different project on a different runtime. The head/tree fingerprints are
// full GitFingerprint values; the script binds the stored compact review
// projection by prefix.
func GTWE2EProcedureInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"op":              map[string]any{"type": "string", "enum": []any{"track_accept"}},
			"listen_addr":     map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"target_project":  map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"planner_session": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"target_track":    map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"track_revision":  map[string]any{"$ref": "Revision"},
			"head":            map[string]any{"$ref": "GitFingerprint"},
			"tree":            map[string]any{"$ref": "GitFingerprint"},
			"submitted_by":    map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"submitted_at":    map[string]any{"$ref": "Timestamp"},
			"tasks": map[string]any{
				"type": "array", "minItems": 0, "maxItems": 64,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{
						"key":      map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
						"revision": map[string]any{"$ref": "Revision"},
					},
					"required": []any{"key", "revision"},
				},
			},
		},
		"required": []any{
			"op", "listen_addr", "target_project", "planner_session",
			"target_track", "track_revision", "head", "tree", "submitted_by", "submitted_at", "tasks",
		},
	}
}

// GTWE2EProcedureOutputSchema is the closed typed receipt returned to Lead:
// the target project, the accepted Track, its resulting revision plus the
// accepted review head/tree and final status. target_track is deliberately
// an opaque bounded string: the executor validates EntityKeyAndReference
// output fields against the OWNING project, and this receipt names a Track
// that only exists on the target runtime (GTW-OPR4017 / TSK695).
func GTWE2EProcedureOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"op":             map[string]any{"type": "string", "enum": []any{"track_accept"}},
			"target_project": map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"target_track":   map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"revision":       map[string]any{"$ref": "Revision"},
			"head":           map[string]any{"$ref": "GitFingerprint"},
			"tree":           map[string]any{"$ref": "GitFingerprint"},
			"status":         map[string]any{"type": "string", "enum": []any{"accepted"}},
		},
		"required": []any{"op", "target_project", "target_track", "revision", "head", "tree", "status"},
	}
}

// GTWE2EProcedureDefinition is the canonical installed definition.
func GTWE2EProcedureDefinition() (ProjectProcedureDefinition, error) {
	definition := ProjectProcedureDefinition{
		Script:  GTWE2EScriptPath,
		Summary: "Accept one exact approved Track review snapshot on a bounded loopback target runtime.",
		Guide:   "Planner-only E2E relay: requires an exact pending review snapshot, verifies the target Planner Session and Track facts, calls track/accept, and returns a typed accepted receipt.",
		Input:   GTWE2EProcedureInputSchema(),
		Output:  GTWE2EProcedureOutputSchema(),
	}
	if err := ValidateProjectProcedureDefinition(definition); err != nil {
		return ProjectProcedureDefinition{}, fmt.Errorf("canonical e2e Procedure definition is invalid: %w", err)
	}
	return definition, nil
}
