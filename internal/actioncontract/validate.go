package actioncontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (compiled *CompiledSet) ValidateInput(path string, value any) error {
	return compiled.validateActionValue(path, value, true)
}

func (compiled *CompiledSet) ValidateOutput(path string, value any) error {
	return compiled.validateActionValue(path, value, false)
}

func (compiled *CompiledSet) validateActionValue(path string, value any, input bool) error {
	if compiled == nil {
		return fmt.Errorf("compiled contract set is nil")
	}
	action, ok := compiled.actions[path]
	if !ok {
		return fmt.Errorf("unknown action %q", path)
	}
	schema := action.Output
	label := "output"
	if input {
		schema = action.Input
		label = "input"
	}
	normalized, err := normalizeJSON(value)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return validateCompiledSchema(schema, normalized, label)
}

func normalizeJSON(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("value is not JSON-compatible: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("value has trailing JSON content")
		}
		return nil, err
	}
	return normalized, nil
}

func validateCompiledSchema(schema *CompiledSchema, value any, path string) error {
	if schema == nil {
		return fmt.Errorf("%s: missing compiled schema", path)
	}
	if schema.Not != nil && validateCompiledSchema(schema.Not, value, path) == nil {
		return fmt.Errorf("%s: value matches excluded schema", path)
	}
	for _, candidate := range schema.AllOf {
		if err := validateCompiledSchema(candidate, value, path); err != nil {
			return err
		}
	}
	if len(schema.AnyOf) > 0 {
		matched := false
		for _, candidate := range schema.AnyOf {
			if validateCompiledSchema(candidate, value, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: no any_of schema matched", path)
		}
	}
	if len(schema.OneOf) > 0 {
		matches := 0
		for _, candidate := range schema.OneOf {
			if validateCompiledSchema(candidate, value, path) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: expected exactly one one_of schema match, got %d", path, matches)
		}
	}
	if schema.If != nil && validateCompiledSchema(schema.If, value, path) == nil {
		if err := validateCompiledSchema(schema.Then, value, path); err != nil {
			return err
		}
	}
	if schema.Type != "" && !compiledTypeMatches(schema.Type, value) {
		return fmt.Errorf("%s: value has wrong type %T", path, value)
	}
	if number, ok := numericValue(value); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%s: number is not finite", path)
		}
		if schema.Minimum != nil && number < *schema.Minimum {
			return fmt.Errorf("%s: number is below minimum", path)
		}
		if schema.Maximum != nil && number > *schema.Maximum {
			return fmt.Errorf("%s: number exceeds maximum", path)
		}
	}
	if len(schema.Enum) > 0 {
		matched := false
		for _, candidate := range schema.Enum {
			if schemaEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s: value is outside enum", path)
		}
	}
	if schema.HasConst && !schemaEqual(schema.Const, value) {
		return fmt.Errorf("%s: value does not match const", path)
	}
	if text, ok := value.(string); ok {
		if schema.Pattern != "" {
			matched, err := regexp.MatchString(schema.Pattern, text)
			if err != nil || !matched {
				return fmt.Errorf("%s: string does not match pattern", path)
			}
		}
		if schema.Format == formatTimestamp && !validTimestamp(text) {
			return fmt.Errorf("%s: invalid compact UTC Timestamp", path)
		}
		length := utf8.RuneCountInString(text)
		if schema.MinLength != nil && length < *schema.MinLength {
			return fmt.Errorf("%s: string is shorter than min_length", path)
		}
		if schema.MaxLength != nil && length > *schema.MaxLength {
			return fmt.Errorf("%s: string exceeds max_length", path)
		}
	}
	if object, ok := value.(map[string]any); ok {
		for _, required := range schema.Required {
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
			child, exists := schema.Properties[key]
			if !exists {
				if schema.AdditionalPropertySchema != nil {
					if err := validateCompiledSchema(schema.AdditionalPropertySchema, object[key], path+"."+key); err != nil {
						return err
					}
					continue
				}
				if schema.AdditionalProperties == nil || !*schema.AdditionalProperties {
					return fmt.Errorf("%s: unknown property %q", path, key)
				}
				continue
			}
			if err := validateCompiledSchema(child, object[key], path+"."+key); err != nil {
				return err
			}
		}
	}
	if values, ok := value.([]any); ok {
		if schema.MinItems != nil && len(values) < *schema.MinItems {
			return fmt.Errorf("%s: array has fewer than min_items", path)
		}
		if schema.MaxItems != nil && len(values) > *schema.MaxItems {
			return fmt.Errorf("%s: array exceeds max_items", path)
		}
		if schema.UniqueItems != nil && *schema.UniqueItems {
			for index := range values {
				for other := 0; other < index; other++ {
					if schemaEqual(values[index], values[other]) {
						return fmt.Errorf("%s: array contains duplicate items", path)
					}
				}
			}
		}
		if schema.Items != nil {
			for index, item := range values {
				if err := validateCompiledSchema(schema.Items, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func compiledTypeMatches(expected string, value any) bool {
	switch expected {
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
		number, ok := numericValue(value)
		return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
	case "number":
		number, ok := numericValue(value)
		return ok && !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return false
	}
}

func numericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(string(number), 64)
		return parsed, err == nil
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func schemaEqual(left, right any) bool {
	if leftNumber, ok := numericValue(left); ok {
		rightNumber, rightOK := numericValue(right)
		return rightOK && leftNumber == rightNumber
	}
	if leftMap, ok := left.(map[string]any); ok {
		rightMap, rightOK := right.(map[string]any)
		if !rightOK || len(leftMap) != len(rightMap) {
			return false
		}
		for key, value := range leftMap {
			other, exists := rightMap[key]
			if !exists || !schemaEqual(value, other) {
				return false
			}
		}
		return true
	}
	if leftList, ok := left.([]any); ok {
		rightList, rightOK := right.([]any)
		if !rightOK || len(leftList) != len(rightList) {
			return false
		}
		for index := range leftList {
			if !schemaEqual(leftList[index], rightList[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}

func validTimestamp(value string) bool {
	if len(value) != 17 || value[2] != '-' || value[5] != '-' || value[8] != 'T' || value[11] != ':' || value[14] != ':' {
		return false
	}
	parts := []int{
		parseDecimal(value[0:2]), parseDecimal(value[3:5]), parseDecimal(value[6:8]),
		parseDecimal(value[9:11]), parseDecimal(value[12:14]), parseDecimal(value[15:17]),
	}
	for _, part := range parts {
		if part < 0 {
			return false
		}
	}
	stamp := time.Date(2000+parts[0], time.Month(parts[1]), parts[2], parts[3], parts[4], parts[5], 0, time.UTC)
	return stamp.Year() == 2000+parts[0] && int(stamp.Month()) == parts[1] && stamp.Day() == parts[2] && stamp.Hour() == parts[3] && stamp.Minute() == parts[4] && stamp.Second() == parts[5]
}

func parseDecimal(value string) int {
	for _, char := range value {
		if char < '0' || char > '9' {
			return -1
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func trimText(value string) string {
	return strings.TrimSpace(value)
}
