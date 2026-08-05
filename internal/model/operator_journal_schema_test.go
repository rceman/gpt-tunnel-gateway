package model

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"
	"unicode/utf8"
)

type operatorJournalSchemaOracle struct {
	root map[string]any
}

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

func loadOperatorJournalSchema(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "operator-journal-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema["$defs"].(map[string]any); !ok {
		t.Fatal("schema has no definitions")
	}
	return schema
}

func operatorJournalSchemaFixture() map[string]any {
	return map[string]any{
		"schema_version": float64(1), "id": "EXM-OPR1", "project_id": "example", "session_id": nil,
		"kind": "user_talk", "summary": "context", "content": map[string]any{
			"decisions": []any{}, "commitments": []any{}, "facts": []any{"fact"}, "assumptions": []any{}, "blockers": []any{}, "unresolved": []any{}, "next_actions": []any{},
		}, "references": map[string]any{
			"plan_sections": []any{}, "adrs": []any{}, "tasks": []any{}, "runs": []any{}, "commits": []any{}, "identities": []any{},
		}, "actor": "owner", "occurred_at": "2026-08-05T12:00:00Z", "recorded_at": "2026-08-05T12:00:00Z",
	}
}

func cloneOperatorJournalSchemaFixture(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func TestOperatorJournalStaticSchemaEvaluatesCompleteParityFixtures(t *testing.T) {
	schema := loadOperatorJournalSchema(t)
	oracle := operatorJournalSchemaOracle{root: schema}
	properties := schema["properties"].(map[string]any)
	if properties["id"].(map[string]any)["pattern"] != OperatorEventIDPattern || properties["supersedes_event_id"].(map[string]any)["pattern"] != OperatorEventIDPattern {
		t.Fatal("static event ID patterns diverge from model pattern")
	}
	adrItems := schema["$defs"].(map[string]any)["adr_ids"].(map[string]any)["items"].(map[string]any)
	compactADRPattern := adrItems["anyOf"].([]any)[1].(map[string]any)["pattern"]
	if compactADRPattern != OperatorCompactADRPattern {
		t.Fatal("static compact ADR pattern diverges from model pattern")
	}
	valid := []struct {
		name  string
		value map[string]any
	}{
		{"normal", operatorJournalSchemaFixture()},
		{"max_event_id", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["id"] = "EXM-OPR9007199254740991"
			value["session_id"] = "session"
			return value
		}()},
		{"max_minus_one_event_id", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["id"] = "EXM-OPR9007199254740990"
			return value
		}()},
		{"correction", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["id"] = "EXM-OPR2"
			value["kind"] = "correction"
			value["supersedes_event_id"] = "EXM-OPR1"
			return value
		}()},
		{"legacy_adr", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["references"].(map[string]any)["adrs"] = []any{"ADR-legacy"}
			return value
		}()},
		{"max_compact_adr", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["references"].(map[string]any)["adrs"] = []any{"EXM-ADR9007199254740991"}
			return value
		}()},
	}
	for _, test := range valid {
		if err := oracle.validate(schema, test.value, "$"); err != nil {
			t.Errorf("valid fixture %s rejected: %v", test.name, err)
		}
	}
	invalid := []struct {
		name  string
		value map[string]any
	}{
		{"correction_without_supersedes", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["kind"] = "correction"
			return value
		}()},
		{"non_correction_with_supersedes", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["supersedes_event_id"] = "EXM-OPR1"
			return value
		}()},
		{"empty_session", func() map[string]any { value := operatorJournalSchemaFixture(); value["session_id"] = ""; return value }()},
		{"wrong_session_type", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["session_id"] = float64(1)
			return value
		}()},
		{"zero_event_id", func() map[string]any { value := operatorJournalSchemaFixture(); value["id"] = "EXM-OPR0"; return value }()},
		{"leading_zero_event_id", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["id"] = "EXM-OPR01"
			return value
		}()},
		{"overflow_event_id", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["id"] = "EXM-OPR9007199254740992"
			return value
		}()},
		{"long_event_id", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["id"] = "EXM-OPR90071992547409910"
			return value
		}()},
		{"zero_compact_adr", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["references"].(map[string]any)["adrs"] = []any{"EXM-ADR0"}
			return value
		}()},
		{"leading_zero_compact_adr", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["references"].(map[string]any)["adrs"] = []any{"EXM-ADR01"}
			return value
		}()},
		{"overflow_compact_adr", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["references"].(map[string]any)["adrs"] = []any{"EXM-ADR9007199254740992"}
			return value
		}()},
		{"malformed_compact_adr", func() map[string]any {
			value := operatorJournalSchemaFixture()
			value["references"].(map[string]any)["adrs"] = []any{"EXM-ADR1-extra"}
			return value
		}()},
	}
	for _, test := range invalid {
		if err := oracle.validate(schema, test.value, "$"); err == nil {
			t.Errorf("invalid fixture %s accepted", test.name)
		}
	}
	if clone := cloneOperatorJournalSchemaFixture(t, valid[0].value); !reflect.DeepEqual(clone, valid[0].value) {
		t.Fatal("fixture clone changed JSON values")
	}
}
