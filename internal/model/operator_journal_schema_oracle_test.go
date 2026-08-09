package model

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"time"
	"unicode/utf8"
)

func (o operatorJournalSchemaOracle) validate(schema map[string]any, value any, path string) error {
	if ref, ok := schema["$ref"].(string); ok {
		const prefix = "#/$defs/"
		if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
			return fmt.Errorf("%s: unsupported reference %q", path, ref)
		}
		name := ref[len(prefix):]
		defs, ok := o.root["$defs"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: schema definitions are missing", path)
		}
		definition, ok := defs[name].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: unresolved reference %q", path, ref)
		}
		return o.validate(definition, value, path)
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		for _, candidate := range anyOf {
			candidateSchema, ok := candidate.(map[string]any)
			if ok && o.validate(candidateSchema, value, path) == nil {
				goto anyOfMatched
			}
		}
		return fmt.Errorf("%s: no anyOf schema matched", path)
	}
anyOfMatched:
	if allOf, ok := schema["allOf"].([]any); ok {
		for _, candidate := range allOf {
			candidateSchema, ok := candidate.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: invalid allOf schema", path)
			}
			if err := o.validate(candidateSchema, value, path); err != nil {
				return err
			}
		}
	}
	if condition, ok := schema["if"].(map[string]any); ok && o.validate(condition, value, path) == nil {
		thenSchema, ok := schema["then"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: invalid then schema", path)
		}
		if err := o.validate(thenSchema, value, path); err != nil {
			return err
		}
	}
	if typeValue, ok := schema["type"]; ok && !operatorJournalSchemaTypeMatches(typeValue, value) {
		return fmt.Errorf("%s: wrong type %T", path, value)
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("%s: const mismatch", path)
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
			return fmt.Errorf("%s: enum mismatch", path)
		}
	}
	if number, ok := value.(float64); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%s: non-finite number", path)
		}
		if minimum, ok := operatorJournalSchemaNumber(schema["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s: below minimum", path)
		}
		if maximum, ok := operatorJournalSchemaNumber(schema["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s: above maximum", path)
		}
	}
	if text, ok := value.(string); ok {
		if pattern, ok := schema["pattern"].(string); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil || !matched {
				return fmt.Errorf("%s: pattern mismatch", path)
			}
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return fmt.Errorf("%s: invalid date-time", path)
			}
		}
		if minimum, ok := operatorJournalSchemaNumber(schema["minLength"]); ok && float64(utf8.RuneCountInString(text)) < minimum {
			return fmt.Errorf("%s: below minLength", path)
		}
		if maximum, ok := operatorJournalSchemaNumber(schema["maxLength"]); ok && float64(utf8.RuneCountInString(text)) > maximum {
			return fmt.Errorf("%s: above maxLength", path)
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range operatorJournalSchemaStrings(schema["required"]) {
			if _, exists := object[required]; !exists {
				return fmt.Errorf("%s: missing %s", path, required)
			}
		}
		for key, child := range object {
			childRaw, exists := properties[key]
			if !exists {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s: unknown property %s", path, key)
				}
				continue
			}
			childSchema, ok := childRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: invalid child schema", path, key)
			}
			if err := o.validate(childSchema, child, path+"."+key); err != nil {
				return err
			}
		}
	}
	if values, ok := value.([]any); ok {
		if minimum, ok := operatorJournalSchemaNumber(schema["minItems"]); ok && float64(len(values)) < minimum {
			return fmt.Errorf("%s: below minItems", path)
		}
		if maximum, ok := operatorJournalSchemaNumber(schema["maxItems"]); ok && float64(len(values)) > maximum {
			return fmt.Errorf("%s: above maxItems", path)
		}
		if itemRaw, exists := schema["items"]; exists {
			itemSchema, ok := itemRaw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: invalid items schema", path)
			}
			for i, item := range values {
				if err := o.validate(itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func operatorJournalSchemaTypeMatches(typeValue any, value any) bool {
	if types, ok := typeValue.([]any); ok {
		for _, candidate := range types {
			if operatorJournalSchemaTypeMatches(candidate, value) {
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
		return ok && math.Trunc(n) == n && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "number":
		n, ok := value.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return false
	}
}

func operatorJournalSchemaNumber(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func operatorJournalSchemaStrings(value any) []string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
