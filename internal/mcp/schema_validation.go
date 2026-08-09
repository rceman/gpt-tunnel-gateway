package mcp

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

func validateOutputValue(schema map[string]any, value any) error {
	return validateSchemaValue(schema, value, "$")
}

func validateSchemaValue(schema map[string]any, value any, path string) error {
	if schema == nil {
		return fmt.Errorf("%s: missing schema", path)
	}
	if options, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, candidate := range options {
			candidateSchema, ok := candidate.(map[string]any)
			if ok && validateSchemaValue(candidateSchema, value, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: no anyOf schema matched", path)
		}
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		for _, candidate := range allOf {
			candidateSchema, ok := candidate.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: invalid allOf schema", path)
			}
			if err := validateSchemaValue(candidateSchema, value, path); err != nil {
				return err
			}
		}
	}
	if condition, ok := schema["if"].(map[string]any); ok && validateSchemaValue(condition, value, path) == nil {
		thenSchema, ok := schema["then"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: invalid then schema", path)
		}
		if err := validateSchemaValue(thenSchema, value, path); err != nil {
			return err
		}
	}
	if expected, ok := schema["type"]; ok && !schemaTypeMatches(expected, value) {
		return fmt.Errorf("%s: value has wrong type %T", path, value)
	}
	if number, ok := value.(float64); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%s: number is not finite", path)
		}
		if minimum, ok := schemaNumber(schema["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s: number is below minimum", path)
		}
		if maximum, ok := schemaNumber(schema["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s: number exceeds maximum", path)
		}
	}
	if options, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, candidate := range options {
			candidateSchema, ok := candidate.(map[string]any)
			if ok && validateSchemaValue(candidateSchema, value, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: expected exactly one matching output shape, got %d", path, matches)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value is outside enum", path)
		}
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("%s: value does not match const", path)
	}
	if text, ok := value.(string); ok {
		if pattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil || !matched {
				return fmt.Errorf("%s: string does not match pattern", path)
			}
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return fmt.Errorf("%s: invalid date-time", path)
			}
		}
		if minimum, ok := schemaNumber(schema["minLength"]); ok && float64(utf8.RuneCountInString(text)) < minimum {
			return fmt.Errorf("%s: string is shorter than minLength", path)
		}
		if maximum, ok := schemaNumber(schema["maxLength"]); ok && float64(utf8.RuneCountInString(text)) > maximum {
			return fmt.Errorf("%s: string exceeds maxLength", path)
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range stringList(schema["required"]) {
			if _, exists := object[required]; !exists {
				return fmt.Errorf("%s: missing required property %q", path, required)
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := object[key]
			childSchemaRaw, exists := properties[key]
			if !exists {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: unknown property %q", path, key)
				}
				continue
			}
			childSchema, ok := childSchemaRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: invalid child schema", path, key)
			}
			if err := validateSchemaValue(childSchema, child, path+"."+key); err != nil {
				return err
			}
		}
	}
	if values, ok := value.([]any); ok {
		if minimum, ok := schemaNumber(schema["minItems"]); ok && float64(len(values)) < minimum {
			return fmt.Errorf("%s: array has fewer than minItems", path)
		}
		if maximum, ok := schemaNumber(schema["maxItems"]); ok && float64(len(values)) > maximum {
			return fmt.Errorf("%s: array exceeds maxItems", path)
		}
		if itemRaw, exists := schema["items"]; exists {
			itemSchema, ok := itemRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: invalid item schema", path)
			}
			for index, item := range values {
				if err := validateSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func schemaTypeMatches(typeValue any, value any) bool {
	if types, ok := typeValue.([]any); ok {
		for _, candidate := range types {
			if schemaTypeMatches(candidate, value) {
				return true
			}
		}
		return false
	}
	typeName, ok := typeValue.(string)
	if !ok {
		return false
	}
	switch typeName {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
	case "number":
		n, ok := value.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return false
	}
}

func schemaNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func stringList(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}
