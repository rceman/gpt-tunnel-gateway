package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type operatorJournalSchemaOracle struct {
	root map[string]any
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
