package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGuideApplicabilityMatrixIsCompleteAndExplained(t *testing.T) {
	matrix := GuideApplicabilityMatrix()
	seen := make(map[string]bool, len(matrix))
	for _, item := range matrix {
		if item.Subject == "" || seen[item.Subject] || strings.TrimSpace(item.Rationale) == "" {
			t.Fatalf("invalid guide applicability row: %#v", item)
		}
		seen[item.Subject] = true
		if item.Required {
			if len(item.ProjectionFields) == 0 {
				t.Fatalf("required subject lacks a bounded projection: %#v", item)
			}
			if item.BuiltinUntilBound && item.Subject != "agent" && item.Subject != "task" {
				t.Fatalf("unexpected transitional builtin: %#v", item)
			}
		} else if len(item.ProjectionFields) != 0 || item.BuiltinUntilBound {
			t.Fatalf("omitted subject has an active guide projection: %#v", item)
		}
	}
	for _, subject := range []string{"adr", "task", "rule", "journal", "milestone", "track", "agent"} {
		if !GuideRequired(subject) {
			t.Fatalf("durable guide subject %q is not required", subject)
		}
	}
	for _, subject := range []string{"bootstrap", "callback", "code", "gate", "message", "operation", "plan", "project", "project_configuration", "relation", "runtime", "session", "system", "task_execution", "train"} {
		if seen[subject] && GuideRequired(subject) {
			t.Fatalf("subject %q should remain intentionally omitted", subject)
		}
		if !seen[subject] {
			t.Fatalf("durable-domain inventory omits rationale for %q", subject)
		}
	}
	if !reflect.DeepEqual(GuideSubjects(), []string{"adr", "agent", "journal", "milestone", "rule", "task", "track"}) {
		t.Fatalf("required subjects=%v", GuideSubjects())
	}
}

func TestGuideBindingsAreGenericCanonicalRuleIdentityOnly(t *testing.T) {
	configuration := DefaultProjectConfiguration("example", time.Date(2026, time.September, 23, 0, 0, 0, 0, time.UTC))
	if configuration.GuideBindings == nil || len(configuration.GuideBindings) != 0 {
		t.Fatalf("default guide bindings=%v", configuration.GuideBindings)
	}
	configuration.GuideBindings = map[string]string{"task": "EXM-RUL12", "track": "EXM-RUL13"}
	if err := ValidateProjectConfiguration(configuration); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ProjectConfiguration
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.GuideBindings, configuration.GuideBindings) {
		t.Fatalf("round-trip guide bindings=%v", decoded.GuideBindings)
	}
	for name, bindings := range map[string]map[string]string{
		"unknown subject": {"project_configuration": "EXM-RUL12"},
		"omitted subject": {"message": "EXM-RUL12"},
		"invalid key":     {"task": "not-a-rule"},
	} {
		candidate := configuration
		candidate.GuideBindings = bindings
		if err := ValidateProjectConfiguration(candidate); err == nil {
			t.Fatalf("%s binding accepted", name)
		}
	}
}

func TestGuideRuleValueProjectionIsClosedBoundedAndDeterministic(t *testing.T) {
	valid := json.RawMessage(`{"workflow":"Planner owns WHAT/WHY.","review":"Lead reviews.","verification":"Lead verifies.","completion":"Planner accepts.","boundaries":"Worker implements and submits once."}`)
	projection, err := ValidateGuideValue("task", valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection) != 5 || projection["workflow"] != "Planner owns WHAT/WHY." {
		t.Fatalf("projection=%v", projection)
	}
	badValues := map[string]json.RawMessage{
		"missing field": json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x"}`),
		"extra field":   json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":"x","policy":"copied body"}`),
		"non-string":    json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":1}`),
		"empty":         json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":" "}`),
		"control":       json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":"bad\u0001value"}`),
		"oversized":     json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":"` + strings.Repeat("a", GuideTextMaxRunes+1) + `"}`),
		"duplicate":     json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":"x","boundaries":"y"}`),
		"trailing":      json.RawMessage(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":"x"} {}`),
	}
	for name, value := range badValues {
		if _, err := ValidateGuideValue("task", value); err == nil {
			t.Fatalf("%s Rule value accepted", name)
		}
	}
	invalidUTF8 := append([]byte(`{"workflow":"x","review":"x","verification":"x","completion":"x","boundaries":"`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	if _, err := ValidateGuideValue("task", json.RawMessage(invalidUTF8)); err == nil {
		t.Fatal("invalid UTF-8 Rule value accepted")
	}
	if _, err := ValidateGuideValue("message", valid); err == nil {
		t.Fatal("intentionally omitted subject accepted a guide projection")
	}
}
