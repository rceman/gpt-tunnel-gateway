package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	OperatorJournalSchemaVersion = 1
	MaxOperatorSummaryBytes      = 4096
	MaxOperatorActorBytes        = 256
	MaxOperatorSessionIDBytes    = 256
	MaxOperatorContentItems      = 128
	MaxOperatorContentItemBytes  = 2048
	MaxOperatorReferenceItems    = 128
	MaxOperatorHistoryLimit      = 200
)

var operatorEventIDRE = regexp.MustCompile(`^([A-Z]{3})-O([1-9][0-9]*)$`)

type OperatorJournalKind string

const (
	OperatorUserTalk         OperatorJournalKind = "user_talk"
	OperatorReasoningSummary OperatorJournalKind = "reasoning_summary"
	OperatorTaskPlan         OperatorJournalKind = "task_plan"
	OperatorTaskReview       OperatorJournalKind = "task_review"
	OperatorOperation        OperatorJournalKind = "operation"
	OperatorCheckpoint       OperatorJournalKind = "checkpoint"
	OperatorCorrection       OperatorJournalKind = "correction"
)

var operatorJournalKinds = map[OperatorJournalKind]bool{
	OperatorUserTalk: true, OperatorReasoningSummary: true, OperatorTaskPlan: true,
	OperatorTaskReview: true, OperatorOperation: true, OperatorCheckpoint: true,
	OperatorCorrection: true,
}

type OperatorJournalContent struct {
	Decisions   []string `json:"decisions"`
	Commitments []string `json:"commitments"`
	Facts       []string `json:"facts"`
	Assumptions []string `json:"assumptions"`
	Blockers    []string `json:"blockers"`
	Unresolved  []string `json:"unresolved"`
	NextActions []string `json:"next_actions"`
}

type OperatorJournalReferences struct {
	PlanSections []string `json:"plan_sections"`
	ADRs         []string `json:"adrs"`
	Tasks        []string `json:"tasks"`
	Runs         []string `json:"runs"`
	Commits      []string `json:"commits"`
	Identities   []string `json:"identities"`
}

type OperatorJournalCounter struct {
	SchemaVersion   int    `json:"schema_version"`
	ProjectID       string `json:"project_id"`
	NextEventNumber uint64 `json:"next_event_number"`
}

type OperatorJournalEvent struct {
	SchemaVersion     int                       `json:"schema_version"`
	ID                string                    `json:"id"`
	ProjectID         string                    `json:"project_id"`
	SessionID         *string                   `json:"session_id"`
	Kind              OperatorJournalKind       `json:"kind"`
	Summary           string                    `json:"summary"`
	Content           OperatorJournalContent    `json:"content"`
	References        OperatorJournalReferences `json:"references"`
	SupersedesEventID string                    `json:"supersedes_event_id,omitempty"`
	Actor             string                    `json:"actor"`
	OccurredAt        time.Time                 `json:"occurred_at"`
	RecordedAt        time.Time                 `json:"recorded_at"`
}

// Short aliases keep callers concise without introducing another journal model.
type OperatorEvent = OperatorJournalEvent
type OperatorContent = OperatorJournalContent
type OperatorReferences = OperatorJournalReferences

func (v OperatorJournalContent) MarshalJSON() ([]byte, error) {
	type wire struct {
		Decisions   []string `json:"decisions"`
		Commitments []string `json:"commitments"`
		Facts       []string `json:"facts"`
		Assumptions []string `json:"assumptions"`
		Blockers    []string `json:"blockers"`
		Unresolved  []string `json:"unresolved"`
		NextActions []string `json:"next_actions"`
	}
	return json.Marshal(wire{canonicalOperatorStrings(v.Decisions), canonicalOperatorStrings(v.Commitments), canonicalOperatorStrings(v.Facts), canonicalOperatorStrings(v.Assumptions), canonicalOperatorStrings(v.Blockers), canonicalOperatorStrings(v.Unresolved), canonicalOperatorStrings(v.NextActions)})
}

func (v OperatorJournalReferences) MarshalJSON() ([]byte, error) {
	type wire struct {
		PlanSections []string `json:"plan_sections"`
		ADRs         []string `json:"adrs"`
		Tasks        []string `json:"tasks"`
		Runs         []string `json:"runs"`
		Commits      []string `json:"commits"`
		Identities   []string `json:"identities"`
	}
	return json.Marshal(wire{canonicalOperatorStrings(v.PlanSections), canonicalOperatorStrings(v.ADRs), canonicalOperatorStrings(v.Tasks), canonicalOperatorStrings(v.Runs), canonicalOperatorStrings(v.Commits), canonicalOperatorStrings(v.Identities)})
}

func (v *OperatorJournalContent) UnmarshalJSON(data []byte) error {
	fields, err := operatorObject(data, "operator journal content")
	if err != nil {
		return err
	}
	keys := []string{"decisions", "commitments", "facts", "assumptions", "blockers", "unresolved", "next_actions"}
	if err := requireOperatorFields(fields, keys...); err != nil {
		return err
	}
	for key := range fields {
		if !containsString(keys, key) {
			return fmt.Errorf("unknown operator journal content field %q", key)
		}
	}
	values := make([][]string, len(keys))
	for i, key := range keys {
		values[i], err = operatorStringArray(fields[key], key)
		if err != nil {
			return err
		}
	}
	*v = OperatorJournalContent{Decisions: values[0], Commitments: values[1], Facts: values[2], Assumptions: values[3], Blockers: values[4], Unresolved: values[5], NextActions: values[6]}
	return nil
}

func (v *OperatorJournalReferences) UnmarshalJSON(data []byte) error {
	fields, err := operatorObject(data, "operator journal references")
	if err != nil {
		return err
	}
	keys := []string{"plan_sections", "adrs", "tasks", "runs", "commits", "identities"}
	if err := requireOperatorFields(fields, keys...); err != nil {
		return err
	}
	for key := range fields {
		if !containsString(keys, key) {
			return fmt.Errorf("unknown operator journal references field %q", key)
		}
	}
	values := make([][]string, len(keys))
	for i, key := range keys {
		values[i], err = operatorStringArray(fields[key], key)
		if err != nil {
			return err
		}
	}
	*v = OperatorJournalReferences{PlanSections: values[0], ADRs: values[1], Tasks: values[2], Runs: values[3], Commits: values[4], Identities: values[5]}
	return nil
}

func (v *OperatorJournalCounter) UnmarshalJSON(data []byte) error {
	fields, err := operatorObject(data, "operator journal counter")
	if err != nil {
		return err
	}
	if err := requireOperatorFields(fields, "schema_version", "project_id", "next_event_number"); err != nil {
		return err
	}
	for key := range fields {
		if key != "schema_version" && key != "project_id" && key != "next_event_number" {
			return fmt.Errorf("unknown operator journal counter field %q", key)
		}
	}
	schemaVersion, err := parseJSONInteger(fields["schema_version"])
	if err != nil || schemaVersion > uint64(^uint(0)>>1) {
		return fmt.Errorf("schema_version: invalid integer")
	}
	var projectID string
	if err := decodeOperatorString(fields["project_id"], &projectID); err != nil {
		return fmt.Errorf("project_id: %w", err)
	}
	next, err := parseJSONInteger(fields["next_event_number"])
	if err != nil {
		return fmt.Errorf("next_event_number: %w", err)
	}
	*v = OperatorJournalCounter{SchemaVersion: int(schemaVersion), ProjectID: projectID, NextEventNumber: next}
	return nil
}

func (v *OperatorJournalEvent) UnmarshalJSON(data []byte) error {
	fields, err := operatorObject(data, "operator journal event")
	if err != nil {
		return err
	}
	required := []string{"schema_version", "id", "project_id", "session_id", "kind", "summary", "content", "references", "actor", "occurred_at", "recorded_at"}
	if err := requireOperatorFields(fields, required...); err != nil {
		return err
	}
	allowed := map[string]bool{"schema_version": true, "id": true, "project_id": true, "session_id": true, "kind": true, "summary": true, "content": true, "references": true, "supersedes_event_id": true, "actor": true, "occurred_at": true, "recorded_at": true}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown operator journal event field %q", key)
		}
	}
	schemaVersion, err := parseJSONInteger(fields["schema_version"])
	if err != nil || schemaVersion > uint64(^uint(0)>>1) {
		return fmt.Errorf("schema_version: invalid integer")
	}
	var id, projectID, kindText, summary, actor string
	for key, target := range map[string]*string{"id": &id, "project_id": &projectID, "kind": &kindText, "summary": &summary, "actor": &actor} {
		if err := decodeOperatorString(fields[key], target); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	var sessionID *string
	if string(fields["session_id"]) != "null" {
		var value string
		if err := decodeOperatorString(fields["session_id"], &value); err != nil {
			return fmt.Errorf("session_id: %w", err)
		}
		sessionID = &value
	}
	var content OperatorJournalContent
	if err := json.Unmarshal(fields["content"], &content); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	var references OperatorJournalReferences
	if err := json.Unmarshal(fields["references"], &references); err != nil {
		return fmt.Errorf("references: %w", err)
	}
	supersedes := ""
	if value, ok := fields["supersedes_event_id"]; ok {
		if err := decodeOperatorString(value, &supersedes); err != nil {
			return fmt.Errorf("supersedes_event_id: %w", err)
		}
	}
	occurred, err := operatorTime(fields["occurred_at"], "occurred_at")
	if err != nil {
		return err
	}
	recorded, err := operatorTime(fields["recorded_at"], "recorded_at")
	if err != nil {
		return err
	}
	*v = OperatorJournalEvent{SchemaVersion: int(schemaVersion), ID: id, ProjectID: projectID, SessionID: sessionID, Kind: OperatorJournalKind(kindText), Summary: summary, Content: content, References: references, SupersedesEventID: supersedes, Actor: actor, OccurredAt: occurred, RecordedAt: recorded}
	return nil
}

func ValidateOperatorJournalCounter(v OperatorJournalCounter) error {
	if v.SchemaVersion != OperatorJournalSchemaVersion {
		return fmt.Errorf("invalid operator journal counter schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateCompactIDNumber(v.NextEventNumber); err != nil {
		return fmt.Errorf("next_event_number: %w", err)
	}
	return nil
}

func ValidateOperatorJournalKind(v OperatorJournalKind) error {
	if !operatorJournalKinds[v] {
		return fmt.Errorf("invalid operator journal kind %q", v)
	}
	return nil
}

func ValidateOperatorJournalContent(v OperatorJournalContent) error {
	values := map[string][]string{"decisions": v.Decisions, "commitments": v.Commitments, "facts": v.Facts, "assumptions": v.Assumptions, "blockers": v.Blockers, "unresolved": v.Unresolved, "next_actions": v.NextActions}
	for name, items := range values {
		if len(items) > MaxOperatorContentItems {
			return fmt.Errorf("%s contains too many entries", name)
		}
		for _, item := range items {
			if err := validateOperatorText(item, MaxOperatorContentItemBytes, name, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v OperatorJournalContent) HasMaterial() bool {
	return len(v.Decisions)+len(v.Commitments)+len(v.Facts)+len(v.Assumptions)+len(v.Blockers)+len(v.Unresolved)+len(v.NextActions) > 0
}

func ValidateOperatorJournalReferences(v OperatorJournalReferences) error {
	values := map[string][]string{"plan_sections": v.PlanSections, "adrs": v.ADRs, "tasks": v.Tasks, "runs": v.Runs, "commits": v.Commits, "identities": v.Identities}
	for name, items := range values {
		if len(items) > MaxOperatorReferenceItems {
			return fmt.Errorf("%s contains too many references", name)
		}
		seen := map[string]bool{}
		for _, item := range items {
			if err := validateOperatorText(item, MaxOperatorContentItemBytes, name, true); err != nil {
				return err
			}
			if seen[item] {
				return fmt.Errorf("duplicate %s reference %q", name, item)
			}
			seen[item] = true
			switch name {
			case "plan_sections":
				if err := ValidateObjectIdentifier(item); err != nil {
					return fmt.Errorf("plan_sections: %w", err)
				}
			case "adrs":
				if err := ValidateADRIdentifier(item); err != nil {
					return fmt.Errorf("adrs: %w", err)
				}
			case "tasks", "runs":
				if err := ValidateObjectIdentifier(item); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			case "commits":
				if err := ValidateCommitSHA(item); err != nil {
					return fmt.Errorf("commits: %w", err)
				}
			}
		}
	}
	return nil
}

func ValidateOperatorJournalEvent(v OperatorJournalEvent) error {
	if v.SchemaVersion != OperatorJournalSchemaVersion {
		return fmt.Errorf("invalid operator journal event schema_version")
	}
	if err := ValidateProjectIdentifier(v.ProjectID); err != nil {
		return err
	}
	if err := ValidateOperatorEventID(v.ID); err != nil {
		return err
	}
	if err := ValidateOperatorJournalKind(v.Kind); err != nil {
		return err
	}
	if err := validateOperatorText(v.Summary, MaxOperatorSummaryBytes, "summary", true); err != nil {
		return err
	}
	if v.SessionID != nil {
		if err := validateOperatorText(*v.SessionID, MaxOperatorSessionIDBytes, "session_id", true); err != nil {
			return err
		}
	}
	if err := ValidateOperatorJournalContent(v.Content); err != nil {
		return err
	}
	if err := ValidateOperatorJournalReferences(v.References); err != nil {
		return err
	}
	if !v.Content.HasMaterial() && !operatorReferencesHaveMaterial(v.References) {
		return fmt.Errorf("operator journal event must contain material content or references")
	}
	if err := validateOperatorText(v.Actor, MaxOperatorActorBytes, "actor", true); err != nil {
		return err
	}
	if v.OccurredAt.IsZero() || v.RecordedAt.IsZero() {
		return fmt.Errorf("operator journal timestamps are required")
	}
	if v.OccurredAt.After(v.RecordedAt) {
		return fmt.Errorf("occurred_at cannot be after recorded_at")
	}
	if v.SupersedesEventID != "" {
		if err := ValidateOperatorEventID(v.SupersedesEventID); err != nil {
			return fmt.Errorf("supersedes_event_id: %w", err)
		}
		if v.Kind != OperatorCorrection {
			return fmt.Errorf("supersedes_event_id requires correction kind")
		}
	} else if v.Kind == OperatorCorrection {
		return fmt.Errorf("correction kind requires supersedes_event_id")
	}
	return nil
}

func FormatOperatorEventID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if err := ValidateCompactIDNumber(number); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-O%d", projectCode, number), nil
}

func ParseOperatorEventID(value string) (string, uint64, error) {
	matches := operatorEventIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid compact operator event ID")
	}
	number, err := parseCompactIDNumber(matches[2])
	if err != nil {
		return "", 0, err
	}
	return matches[1], number, nil
}

func ValidateOperatorEventID(value string) error {
	_, _, err := ParseOperatorEventID(value)
	return err
}

func ValidateOperatorEventIDForProject(value, projectCode string) error {
	if err := ValidateProjectCode(projectCode); err != nil {
		return err
	}
	code, _, err := ParseOperatorEventID(value)
	if err != nil {
		return err
	}
	if code != projectCode {
		return fmt.Errorf("operator event ID project code %q does not match expected project code %q", code, projectCode)
	}
	return nil
}

func operatorObject(data []byte, name string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("%s must be an object", name)
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key := keyToken.(string)
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate %s field %q", name, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("%s is not closed", name)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%s has trailing JSON content", name)
	}
	return fields, nil
}

func requireOperatorFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("operator journal field %q is required", name)
		}
	}
	return nil
}

func decodeOperatorString(data []byte, out *string) error {
	if string(data) == "null" {
		return fmt.Errorf("must be a string")
	}
	return json.Unmarshal(data, out)
}

func operatorStringArray(data []byte, name string) ([]string, error) {
	if string(data) == "null" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
	return canonicalOperatorStrings(values), nil
}

func operatorTime(data []byte, name string) (time.Time, error) {
	var value string
	if err := decodeOperatorString(data, &value); err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", name, err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: invalid RFC3339 timestamp", name)
	}
	return parsed, nil
}

func validateOperatorText(value string, max int, name string, nonEmpty bool) error {
	if !utf8.ValidString(value) || len([]byte(value)) > max || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("invalid or oversized operator journal %s", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("operator journal %s contains control characters", name)
		}
	}
	if nonEmpty && value == "" {
		return fmt.Errorf("operator journal %s must be non-empty", name)
	}
	return nil
}

func canonicalOperatorStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func operatorReferencesHaveMaterial(v OperatorJournalReferences) bool {
	return len(v.PlanSections)+len(v.ADRs)+len(v.Tasks)+len(v.Runs)+len(v.Commits)+len(v.Identities) > 0
}
