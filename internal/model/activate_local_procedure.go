package model

import "fmt"

// ActivateLocalProcedureName is the canonical GTW-owned live activation
// Procedure. It is invoked directly through procedure/activate_local (no Hook
// binding); the trusted repo-local script owns GTW-specific build, install,
// restart, migration, and readiness orchestration for the exact source an
// accepted Track authorizes.
const ActivateLocalProcedureName = "activate_local"

// ActivateLocalProcedureChecks is the fixed ordered evidence set the trusted
// GTW activation script must emit.
var ActivateLocalProcedureChecks = []string{
	"authority_bind", "source_bind", "preflight", "release_build", "cutover", "verify",
}

// ActivateLocalProcedureInputSchema binds the accepted Track and the exact
// source identity the caller claims activation authority for.
func ActivateLocalProcedureInputSchema() map[string]any {
	fingerprint := map[string]any{"$ref": "GitFingerprint"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"track":         map[string]any{"$ref": "EntityKeyAndReference"},
			"source_commit": fingerprint,
			"source_tree":   fingerprint,
		},
		"required":             []any{"track", "source_commit", "source_tree"},
		"additionalProperties": false,
	}
}

// ActivateLocalProcedureOutputSchema describes the compact bounded live
// activation evidence: the accepted Track/source authority, the activated
// runtime version and preserved Tunnel process identity, whether a binary
// cutover ran, and one record per check.
func ActivateLocalProcedureOutputSchema(checkIDs []string) (map[string]any, error) {
	if len(checkIDs) == 0 || len(checkIDs) > MaxTaskVerificationProcedureGates {
		return nil, fmt.Errorf("invalid activate_local check count")
	}
	seen := make(map[string]struct{}, len(checkIDs))
	enum := make([]any, len(checkIDs))
	for index, id := range checkIDs {
		if err := ValidateProcedureName(id); err != nil {
			return nil, fmt.Errorf("invalid activate_local check id")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate activate_local check id")
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
			"track":           map[string]any{"$ref": "EntityKeyAndReference"},
			"source_commit":   fingerprintOutput(),
			"source_tree":     fingerprintOutput(),
			"gateway_version": map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"tunnel_pid":      map[string]any{"type": "integer", "minimum": 1, "maximum": 4194304},
			"restarted":       map[string]any{"type": "boolean"},
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
		"required":             []any{"track", "source_commit", "source_tree", "gateway_version", "tunnel_pid", "restarted", "checks"},
		"additionalProperties": false,
	}, nil
}
