package model

import "fmt"

// ReleaseProdProcedureName is the canonical GTW-owned production release
// Procedure. It is invoked directly through procedure/release_prod (no Hook
// binding); the trusted repo-local script owns GTW-specific tag/publish and
// external-delivery provenance for the exact source an accepted Track
// authorizes.
const ReleaseProdProcedureName = "release_prod"

// ReleaseProdProcedureChecks is the fixed ordered evidence set the trusted
// GTW release script must emit.
var ReleaseProdProcedureChecks = []string{
	"authority_bind", "source_bind", "tag_ready", "tag", "publish", "verify",
}

// ReleaseProdProcedureInputSchema binds the accepted Track and the exact
// source identity the caller claims release authority for.
func ReleaseProdProcedureInputSchema() map[string]any {
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

// ReleaseProdProcedureOutputSchema describes the compact bounded external
// delivery provenance: the accepted Track/source authority, the published
// version/tag identities, and one record per check.
func ReleaseProdProcedureOutputSchema(checkIDs []string) (map[string]any, error) {
	if len(checkIDs) == 0 || len(checkIDs) > MaxTaskVerificationProcedureGates {
		return nil, fmt.Errorf("invalid release_prod check count")
	}
	seen := make(map[string]struct{}, len(checkIDs))
	enum := make([]any, len(checkIDs))
	for index, id := range checkIDs {
		if err := ValidateProcedureName(id); err != nil {
			return nil, fmt.Errorf("invalid release_prod check id")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate release_prod check id")
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
			"track":         map[string]any{"$ref": "EntityKeyAndReference"},
			"source_commit": fingerprintOutput(),
			"source_tree":   fingerprintOutput(),
			"version":       map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"tag":           map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
			"tag_object":    fingerprintOutput(),
			"published":     map[string]any{"type": "boolean"},
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
		"required":             []any{"track", "source_commit", "source_tree", "version", "tag", "tag_object", "published", "checks"},
		"additionalProperties": false,
	}, nil
}
