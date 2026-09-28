package model

import (
	"fmt"
)

const (
	MaxTaskVerificationProcedureGates = 16
	MaxTaskVerificationGateDurationMS = 30 * 60 * 1000
)

func TaskVerificationProcedureOutputSchema(gateIDs []string) (map[string]any, error) {
	if len(gateIDs) == 0 || len(gateIDs) > MaxTaskVerificationProcedureGates {
		return nil, fmt.Errorf("invalid Task verification Procedure gate count")
	}
	seen := make(map[string]struct{}, len(gateIDs))
	enum := make([]any, len(gateIDs))
	for index, id := range gateIDs {
		if err := ValidateProcedureName(id); err != nil {
			return nil, fmt.Errorf("invalid Task verification Procedure gate id")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate Task verification Procedure gate id")
		}
		seen[id] = struct{}{}
		enum[index] = id
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"gates": map[string]any{
				"type":     "array",
				"minItems": len(gateIDs),
				"maxItems": len(gateIDs),
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
		"required":             []any{"gates"},
		"additionalProperties": false,
	}, nil
}
