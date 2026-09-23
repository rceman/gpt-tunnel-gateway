package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const GuideTextMaxRunes = 768

type GuideApplicability struct {
	Subject           string
	Required          bool
	BuiltinUntilBound bool
	Rationale         string
	ProjectionFields  []string
}

var guideApplicability = []GuideApplicability{
	{Subject: "adr", Required: true, Rationale: "Architecture decisions have proposal, acceptance, relation, and supersession workflows.", ProjectionFields: []string{"guidance"}},
	{Subject: "agent", Required: true, BuiltinUntilBound: true, Rationale: "Role supervision has project workflow semantics; the builtin is transitional until its Rule binding exists.", ProjectionFields: []string{"role_authority", "delegation", "startup", "canonical_state", "exploration_budget", "stop_fast", "checkpoints", "testing", "execution_example", "cli_usage", "architecture", "tail", "status_await", "prompt_interrupt", "authority"}},
	{Subject: "journal", Required: true, Rationale: "Durable journal writing has stream-specific rules; this guide directs callers to journal/contract without copying its schemas.", ProjectionFields: []string{"guidance"}},
	{Subject: "milestone", Required: true, Rationale: "Milestones group durable planning work and have explicit activation, completion, and membership semantics.", ProjectionFields: []string{"guidance"}},
	{Subject: "rule", Required: true, Rationale: "Rules are canonical policy and have proposal, acceptance, revision, and archival workflows.", ProjectionFields: []string{"guidance"}},
	{Subject: "task", Required: true, BuiltinUntilBound: true, Rationale: "Task authoring and execution have a multi-stage workflow; the builtin is transitional until its Rule binding exists.", ProjectionFields: []string{"workflow", "review", "verification", "completion", "boundaries"}},
	{Subject: "track", Required: true, Rationale: "Tracks compose ordered Task membership and have delegation, submission, and Planner acceptance semantics.", ProjectionFields: []string{"guidance"}},
	{Subject: "bootstrap", Rationale: "Session bootstrap is a closed authentication contract, not a workflow policy entity."},
	{Subject: "callback", Rationale: "Callbacks are typed integration endpoints governed by the callback registration and event contracts."},
	{Subject: "code", Rationale: "Code actions are bounded repository projections, not durable semantic entities."},
	{Subject: "gate", Rationale: "ADR72 is the sole gate taxonomy authority; gate receipts are evidence, not authored guide policy."},
	{Subject: "message", Rationale: "Messages are a durable transport primitive; when to send one belongs to the Agent and Task guides, while message actions define the data contract."},
	{Subject: "operation", Rationale: "Operations are mutation receipts with bounded read/await contracts, not authored workflow policy."},
	{Subject: "plan", Rationale: "The legacy Plan surface is retired; current semantic planning is represented by ADRs, Tasks, and Tracks."},
	{Subject: "project", Rationale: "Project status is a bounded read model; durable workflow authority belongs to project Rules and entity guides."},
	{Subject: "project_configuration", Rationale: "Project configuration is a typed settings document, not an independent workflow policy authority."},
	{Subject: "relation", Rationale: "Relations are closed typed links whose valid kinds and endpoints are defined by their action and model contracts."},
	{Subject: "runtime", Rationale: "Runtime details remain Airelay-owned operational state, not Gateway guide policy."},
	{Subject: "session", Rationale: "Session authority is determined by the authenticated durable Session and its closed bootstrap/action contract."},
	{Subject: "system", Rationale: "System and Gateway diagnostics describe infrastructure state, not project workflow policy."},
	{Subject: "task_execution", Rationale: "Task execution is canonical state read through Task lifecycle actions and is governed by the Task guide."},
	{Subject: "train", Rationale: "Train is historical-only and has no active workflow surface."},
}

func GuideApplicabilityMatrix() []GuideApplicability {
	result := make([]GuideApplicability, len(guideApplicability))
	copy(result, guideApplicability)
	for index := range result {
		result[index].ProjectionFields = append([]string(nil), result[index].ProjectionFields...)
	}
	return result
}

func GuideRequired(subject string) bool {
	for _, item := range guideApplicability {
		if item.Subject == subject {
			return item.Required
		}
	}
	return false
}

func GuideBuiltinUntilBound(subject string) bool {
	for _, item := range guideApplicability {
		if item.Subject == subject {
			return item.Required && item.BuiltinUntilBound
		}
	}
	return false
}

func GuideSubjects() []string {
	result := make([]string, 0, len(guideApplicability))
	for _, item := range guideApplicability {
		if item.Required {
			result = append(result, item.Subject)
		}
	}
	return result
}

func GuideProjectionFields(subject string) ([]string, bool) {
	for _, item := range guideApplicability {
		if item.Subject == subject && item.Required {
			return append([]string(nil), item.ProjectionFields...), true
		}
	}
	return nil, false
}

func ValidateGuideBindings(bindings map[string]string) error {
	for subject, ruleID := range bindings {
		if !GuideRequired(subject) || ValidateRuleID(ruleID) != nil {
			return fmt.Errorf("invalid project guide binding for %q", subject)
		}
	}
	return nil
}

func ValidateGuideValue(subject string, value json.RawMessage) (map[string]string, error) {
	fields, ok := GuideProjectionFields(subject)
	if !ok {
		return nil, fmt.Errorf("guide subject %q is not applicable", subject)
	}
	if !utf8.Valid(value) {
		return nil, fmt.Errorf("guide Rule value must be valid UTF-8")
	}
	object, err := decodeGuideObject(value)
	if err != nil {
		return nil, err
	}
	if len(object) != len(fields) {
		return nil, fmt.Errorf("guide Rule value does not match the %s projection", subject)
	}
	projected := make(map[string]string, len(fields))
	for _, field := range fields {
		raw, exists := object[field]
		if !exists {
			return nil, fmt.Errorf("guide Rule value is missing %q", field)
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || !validGuideProjectionText(text) {
			return nil, fmt.Errorf("guide Rule value field %q is invalid", field)
		}
		projected[field] = text
	}
	return projected, nil
}

func validGuideProjectionText(value string) bool {
	if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > GuideTextMaxRunes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t' {
			return false
		}
	}
	return true
}

func decodeGuideObject(value []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("guide Rule value must be an object")
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid guide Rule value")
		}
		field, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid guide Rule value field")
		}
		if _, duplicate := object[field]; duplicate {
			return nil, fmt.Errorf("duplicate guide Rule value field %q", field)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("invalid guide Rule value field %q", field)
		}
		object[field] = raw
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("invalid guide Rule value object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("guide Rule value has trailing data")
	}
	return object, nil
}
