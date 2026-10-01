package model

import "fmt"

// ActivationPreflightProcedureName is the canonical GTW-owned disposable
// activation preflight Procedure. It is invoked directly through
// procedure/activation_preflight (no Hook binding) during Track execution.
const ActivationPreflightProcedureName = "activation_preflight"

// ActivationPreflightProcedureChecks is the fixed ordered evidence set the
// trusted GTW preflight script must emit.
var ActivationPreflightProcedureChecks = []string{
	"source_bind", "snapshot", "candidate_build", "boot", "e2e", "reopen",
}

// ActivationPreflightProcedureInputSchema binds the caller's expected source
// identity for the exact clean checkout under test.
func ActivationPreflightProcedureInputSchema() map[string]any {
	fingerprint := map[string]any{"$ref": "GitFingerprint"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_commit": fingerprint,
			"source_tree":   fingerprint,
		},
		"required":             []any{"source_commit", "source_tree"},
		"additionalProperties": false,
	}
}

// ActivationPreflightProcedureOutputSchema describes the compact bounded
// source-bound evidence returned by the preflight script: the exact source
// commit/tree, the candidate runtime version, and one record per check.
func ActivationPreflightProcedureOutputSchema(checkIDs []string) (map[string]any, error) {
	if len(checkIDs) == 0 || len(checkIDs) > MaxTaskVerificationProcedureGates {
		return nil, fmt.Errorf("invalid activation preflight check count")
	}
	seen := make(map[string]struct{}, len(checkIDs))
	enum := make([]any, len(checkIDs))
	for index, id := range checkIDs {
		if err := ValidateProcedureName(id); err != nil {
			return nil, fmt.Errorf("invalid activation preflight check id")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate activation preflight check id")
		}
		seen[id] = struct{}{}
		enum[index] = id
	}
	fingerprintOutput := func() map[string]any {
		return map[string]any{"$ref": "GitFingerprint"}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_commit":   fingerprintOutput(),
			"source_tree":     fingerprintOutput(),
			"gateway_version": map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"checks": map[string]any{
				"type":     "array",
				"minItems": len(checkIDs),
				"maxItems": len(checkIDs),
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":          map[string]any{"type": "string", "enum": enum},
						"exit_code":   map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
						"duration_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": MaxTaskVerificationGateDurationMS},
					},
					"required":             []any{"id", "exit_code", "duration_ms"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"source_commit", "source_tree", "gateway_version", "checks"},
		"additionalProperties": false,
	}, nil
}
