package actioncontract

import (
	"fmt"
	"strings"
	"time"
)

func compileFieldMappings(path, name string, specs []fieldMappingSpec) ([]FieldMapping, error) {
	mappings := make([]FieldMapping, 0, len(specs))
	fromSeen := make(map[string]bool, len(specs))
	toSeen := make(map[string]bool, len(specs))
	for index, spec := range specs {
		from, to := strings.TrimSpace(spec.From), strings.TrimSpace(spec.To)
		fromParts, toParts := strings.Split(from, "."), strings.Split(to, ".")
		if !validMappingPath(from) || !validMappingPath(to) || len(fromParts) != len(toParts) {
			return nil, fmt.Errorf("action %s.metadata.%s[%d] has an invalid field path", path, name, index)
		}
		for part := range fromParts {
			if (fromParts[part] == "*") != (toParts[part] == "*") || part < len(fromParts)-1 && fromParts[part] != toParts[part] {
				return nil, fmt.Errorf("action %s.metadata.%s[%d] must rename one field at the same object path", path, name, index)
			}
		}
		if fromParts[len(fromParts)-1] == toParts[len(toParts)-1] || fromSeen[from] || toSeen[to] {
			return nil, fmt.Errorf("action %s.metadata.%s[%d] duplicates or does not change a field", path, name, index)
		}
		fromSeen[from], toSeen[to] = true, true
		mappings = append(mappings, FieldMapping{
			From: from,
			To:   to,
		})
	}
	return mappings, nil
}

func validMappingPath(path string) bool {
	if path == "" || len(path) > 256 {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		if part != "*" && !validPropertyName(part) {
			return false
		}
	}
	return true
}

func validateFieldMappingPaths(actionPath, direction string, mappings []FieldMapping, schema *CompiledSchema, checkFrom bool) error {
	for _, mapping := range mappings {
		path := mapping.To
		if checkFrom {
			path = mapping.From
		}
		if !compiledSchemaHasPath(schema, strings.Split(path, ".")) {
			return fmt.Errorf("action %s.metadata.%s field %q is not declared by the contract", actionPath, direction, path)
		}
	}
	return nil
}

func compiledSchemaHasPath(schema *CompiledSchema, path []string) bool {
	if schema == nil || schema.JSONValue {
		return false
	}
	if len(path) == 0 {
		return true
	}
	if path[0] == "*" {
		return schema.Items != nil && compiledSchemaHasPath(schema.Items, path[1:])
	}
	if child, ok := schema.Properties[path[0]]; ok && compiledSchemaHasPath(child, path[1:]) {
		return true
	}
	for _, branch := range append(append(append([]*CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if compiledSchemaHasPath(branch, path) {
			return true
		}
	}
	return false
}

func (compiled *CompiledSet) AdaptInput(path string, value any) (any, error) {
	action, ok := compiled.Action(path)
	if !ok {
		return nil, fmt.Errorf("unknown action %q", path)
	}
	return applyFieldMappings(value, action.Metadata.HandlerInputMappings)
}

func (compiled *CompiledSet) AdaptOutput(path string, value any) (any, error) {
	action, ok := compiled.Action(path)
	if !ok {
		return nil, fmt.Errorf("unknown action %q", path)
	}
	return applyFieldMappings(value, action.Metadata.HandlerOutputMappings)
}

func applyFieldMappings(value any, mappings []FieldMapping) (any, error) {
	result, err := normalizeJSON(value)
	if err != nil {
		return nil, err
	}
	for _, mapping := range mappings {
		from, to := strings.Split(mapping.From, "."), strings.Split(mapping.To, ".")
		if err := renameFieldAtPath(result, from, to); err != nil {
			return nil, fmt.Errorf("map handler field %q to %q: %w", mapping.From, mapping.To, err)
		}
	}
	return result, nil
}

func renameFieldAtPath(value any, from, to []string) error {
	if len(from) == 1 {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		current, exists := object[from[0]]
		if !exists {
			return nil
		}
		if _, exists := object[to[0]]; exists {
			return fmt.Errorf("target field already exists")
		}
		delete(object, from[0])
		object[to[0]] = current
		return nil
	}
	if from[0] == "*" {
		items, ok := value.([]any)
		if !ok {
			return nil
		}
		for _, item := range items {
			if err := renameFieldAtPath(item, from[1:], to[1:]); err != nil {
				return err
			}
		}
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	child, exists := object[from[0]]
	if !exists {
		return nil
	}
	return renameFieldAtPath(child, from[1:], to[1:])
}

func (compiled *CompiledSet) ProjectOutput(path string, value any) (any, error) {
	action, ok := compiled.Action(path)
	if !ok {
		return nil, fmt.Errorf("unknown action %q", path)
	}
	normalized, err := normalizeJSON(value)
	if err != nil {
		return nil, err
	}
	return projectCompiledOutput(action.Output, normalized)
}

func projectCompiledOutput(schema *CompiledSchema, value any) (any, error) {
	if schema == nil || schema.JSONValue {
		return value, nil
	}
	if schema.RefName == "Timestamp" {
		return compactTimestamp(value)
	}
	if object, ok := value.(map[string]any); ok {
		result := make(map[string]any, len(object))
		for key, child := range object {
			childSchema := schema.Properties[key]
			projected, err := projectCompiledOutput(childSchema, child)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			result[key] = projected
		}
		value = result
	} else if items, ok := value.([]any); ok && schema.Items != nil {
		result := make([]any, len(items))
		for index, child := range items {
			projected, err := projectCompiledOutput(schema.Items, child)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", index, err)
			}
			result[index] = projected
		}
		value = result
	}
	if len(schema.AllOf) > 0 {
		for _, branch := range schema.AllOf {
			projected, err := projectCompiledOutput(branch, value)
			if err != nil {
				return nil, err
			}
			value = projected
		}
	}
	for _, branches := range [][]*CompiledSchema{schema.OneOf, schema.AnyOf} {
		if len(branches) == 0 {
			continue
		}
		var selected any
		matches := 0
		for _, branch := range branches {
			candidate, err := projectCompiledOutput(branch, value)
			if err == nil && validateCompiledSchema(branch, candidate, "output") == nil {
				selected = candidate
				matches++
			}
		}
		if matches == 1 || len(schema.OneOf) == 0 && matches > 0 {
			value = selected
		}
	}
	return value, nil
}

func compactTimestamp(value any) (any, error) {
	var stamp time.Time
	switch typed := value.(type) {
	case time.Time:
		stamp = typed
	case string:
		if validTimestamp(typed) {
			return typed, nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			return nil, fmt.Errorf("invalid Timestamp value")
		}
		stamp = parsed
	default:
		return value, nil
	}
	return stamp.UTC().Format("06-01-02T15:04:05"), nil
}

func compactSchema(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if key != "description" {
				result[key] = compactSchema(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, child := range current {
			result[index] = compactSchema(child)
		}
		return result
	default:
		return value
	}
}
