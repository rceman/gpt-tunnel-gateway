package mcp

import (
	"fmt"
	"sync"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
)

var contractDefinitionsOnce sync.Once
var contractDefinitions *actioncontract.CompiledSet
var contractDefinitionsErr error

func compiledSharedDefinition(name string) map[string]any {
	contractDefinitionsOnce.Do(func() {
		contractDefinitions, contractDefinitionsErr = actioncontract.LoadCanonical()
	})
	if contractDefinitionsErr != nil {
		panic(fmt.Errorf("load canonical shared definitions: %w", contractDefinitionsErr))
	}
	schema, ok := contractDefinitions.DefinitionSchema(name)
	if !ok {
		panic(fmt.Errorf("canonical shared definition %q is missing", name))
	}
	return schema
}

func outputString() map[string]any  { return map[string]any{"type": "string"} }
func outputBoolean() map[string]any { return map[string]any{"type": "boolean"} }
func outputInteger() map[string]any { return map[string]any{"type": "integer"} }
func outputDateTime() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}
func publicGitFingerprintOutputSchema() map[string]any {
	return compiledSharedDefinition("GitFingerprint")
}
func publicServerCursorSchema() map[string]any {
	return compiledSharedDefinition("CursorAndCompactHandle")
}
func publicGitFingerprintOrEmptyOutputSchema() map[string]any {
	return map[string]any{"anyOf": []any{publicGitFingerprintOutputSchema(), map[string]any{"type": "string", "const": ""}}}
}
func publicGitFingerprintExclusionSchema() map[string]any {
	return map[string]any{"anyOf": []any{
		map[string]any{"type": "string", "pattern": `^[0-9a-fA-F]{9,}$`},
		map[string]any{"type": "string", "pattern": `^[0-9a-fA-F]{8}$`, "not": map[string]any{"type": "string", "pattern": `^[0-9a-f]{8}$`}},
	}}
}

func outputArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
func outputEnum(values ...string) map[string]any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return map[string]any{"type": "string", "enum": items}
}
func closedOutput(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func relationGroupedOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "object", "additionalProperties": outputString()}}
}
