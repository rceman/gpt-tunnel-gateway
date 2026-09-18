package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func tsk384BaseRule() Rule {
	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	return Rule{
		SchemaVersion: SchemaVersion,
		ID:            "EXM-RUL1",
		ProjectID:     "example",
		Revision:      1,
		Title:         "Rule title",
		Summary:       "Rule summary",
		Status:        RuleStatusProposed,
		Name:          "ci.release",
		Value:         json.RawMessage(`"release"`),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func TestTSK384RuleNameSyntaxIsClosed(t *testing.T) {
	for _, name := range []string{"integration_branch", "ci.release", "a", "a.b.c", "x9_y", "gate_42", "a.b"} {
		if err := ValidateRuleName(name); err != nil {
			t.Fatalf("valid rule name %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "UPPER", "Ci.Release", "ci..release", ".ci", "ci.", "ci-release", "ci release", "ci/release", "ci@release", "é", strings.Repeat("a", RuleNameMaxRunes+1)} {
		if err := ValidateRuleName(name); err == nil {
			t.Fatalf("invalid rule name %q accepted", name)
		}
	}
}

func TestTSK384RuleFormsAreClosed(t *testing.T) {
	machine := tsk384BaseRule()
	if err := ValidateRule(machine); err != nil {
		t.Fatalf("machine rule rejected: %v", err)
	}
	machine.Description = "narrative alongside a named rule"
	if err := ValidateRule(machine); err != nil {
		t.Fatalf("named rule with description rejected: %v", err)
	}
	narrative := tsk384BaseRule()
	narrative.Name, narrative.Value = "", nil
	narrative.Description = "Narrative rule body."
	if err := ValidateRule(narrative); err != nil {
		t.Fatalf("narrative rule rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Rule){
		"empty rule":         func(r *Rule) { r.Name, r.Value, r.Description = "", nil, "" },
		"name without value": func(r *Rule) { r.Value = nil },
		"value without name": func(r *Rule) { r.Name = ""; r.Description = "body" },
		"unnamed rule with value": func(r *Rule) {
			r.Name = ""
			r.Description = "body"
			r.Value = json.RawMessage(`true`)
		},
		"name and value without description is valid machine form": func(r *Rule) { r.Description = "" },
		"null value":            func(r *Rule) { r.Value = json.RawMessage(`null`) },
		"invalid json value":    func(r *Rule) { r.Value = json.RawMessage(`{`) },
		"oversized description": func(r *Rule) { r.Description = strings.Repeat("x", RuleDescriptionMaxRunes+1) },
		"oversized value":       func(r *Rule) { r.Value = json.RawMessage(`"` + strings.Repeat("x", RuleValueMaxBytes) + `"`) },
		"whitespace title":      func(r *Rule) { r.Title = "  " },
		"oversized title":       func(r *Rule) { r.Title = strings.Repeat("t", RuleTitleMaxRunes+1) },
		"oversized summary":     func(r *Rule) { r.Summary = strings.Repeat("s", RuleSummaryMaxRunes+1) },
		"invalid status":        func(r *Rule) { r.Status = "draft" },
	} {
		rule := tsk384BaseRule()
		mutate(&rule)
		err := ValidateRule(rule)
		if name == "name and value without description is valid machine form" {
			if err != nil {
				t.Fatalf("%s rejected: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestTSK384RuleTypedValuesArePreserved(t *testing.T) {
	for _, value := range []string{`true`, `false`, `42`, `"disabled"`, `{"mode":"strict"}`, `[1,2,3]`} {
		rule := tsk384BaseRule()
		rule.Value = json.RawMessage(value)
		if err := ValidateRule(rule); err != nil {
			t.Fatalf("typed value %s rejected: %v", value, err)
		}
	}
}
