package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOperatorEvent(kind OperatorJournalKind, id string) OperatorJournalEvent {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	return OperatorJournalEvent{
		SchemaVersion: SchemaVersion, ID: id, ProjectID: "example", SessionID: nil,
		Kind: kind, Summary: "bounded operator context",
		Content:    OperatorJournalContent{Facts: []string{"verified fact"}},
		References: OperatorJournalReferences{}, Actor: "owner", OccurredAt: now, RecordedAt: now,
	}
}

func TestOperatorJournalKindParityAndStrictValidation(t *testing.T) {
	for _, kind := range []OperatorJournalKind{OperatorUserTalk, OperatorReasoningSummary, OperatorTaskPlan, OperatorTaskReview, OperatorOperation, OperatorCheckpoint, OperatorCorrection} {
		if kind == OperatorCorrection {
			event := testOperatorEvent(kind, "EXM-O2")
			event.SupersedesEventID = "EXM-O1"
			if err := ValidateOperatorJournalEvent(event); err != nil {
				t.Fatalf("correction rejected: %v", err)
			}
			continue
		}
		if err := ValidateOperatorJournalEvent(testOperatorEvent(kind, "EXM-O1")); err != nil {
			t.Fatalf("kind %q rejected: %v", kind, err)
		}
	}
	if err := ValidateOperatorJournalEvent(testOperatorEvent(OperatorJournalKind("unknown"), "EXM-O1")); err == nil {
		t.Fatal("unknown operator kind accepted")
	}
	if err := ValidateOperatorJournalEvent(testOperatorEvent(OperatorCorrection, "EXM-O1")); err == nil {
		t.Fatal("correction without supersession accepted")
	}
}

func TestOperatorJournalIDsAreNumericAndBounded(t *testing.T) {
	code, number, err := ParseOperatorEventID("EXM-O2")
	if err != nil || code != "EXM" || number != 2 {
		t.Fatalf("unexpected O2 parse: %q %d %v", code, number, err)
	}
	_, number, err = ParseOperatorEventID("EXM-O10")
	if err != nil || number != 10 {
		t.Fatalf("unexpected O10 parse: %d %v", number, err)
	}
	_, number, err = ParseOperatorEventID("EXM-O9007199254740991")
	if err != nil || number != MaxSafeInteger {
		t.Fatalf("unexpected maximum operator event parse: %d %v", number, err)
	}
	_, number, err = ParseADRID("EXM-A9007199254740991")
	if err != nil || number != MaxSafeInteger {
		t.Fatalf("unexpected maximum ADR parse: %d %v", number, err)
	}
	if _, _, err := ParseOperatorEventID("EXM-O01"); err == nil {
		t.Fatal("leading-zero event ID accepted")
	}
	if _, _, err := ParseOperatorEventID("EXM-O9007199254740992"); err == nil {
		t.Fatal("overflow event ID accepted")
	}
}

func TestOperatorJournalRejectsDuplicateUnknownAndNoOpJSON(t *testing.T) {
	base := `{"schema_version":1,"id":"EXM-O1","project_id":"example","session_id":null,"kind":"user_talk","summary":"context","content":{"decisions":[],"commitments":[],"facts":["fact"],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":[]},"references":{"plan_sections":[],"adrs":[],"tasks":[],"runs":[],"commits":[],"identities":[]},"actor":"owner","occurred_at":"2026-08-05T12:00:00Z","recorded_at":"2026-08-05T12:00:00Z"}`
	var event OperatorJournalEvent
	if err := json.Unmarshal([]byte(base), &event); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if event.Content.Facts == nil || event.References.Commits == nil {
		t.Fatal("nil arrays were not canonicalized")
	}
	if !strings.Contains(string(mustOperatorJSON(event)), `"facts":["fact"]`) {
		t.Fatal("event did not marshal canonical content")
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(base, "}")+`,"unknown":true}`), &event); err == nil {
		t.Fatal("unknown event field accepted")
	}
	if err := json.Unmarshal([]byte(strings.Replace(base, `"actor":"owner"`, `"actor":"owner","actor":"other"`, 1)), &event); err == nil {
		t.Fatal("duplicate event field accepted")
	}
	noOp := testOperatorEvent(OperatorUserTalk, "EXM-O1")
	noOp.Content = OperatorJournalContent{}
	if err := ValidateOperatorJournalEvent(noOp); err == nil {
		t.Fatal("empty content and references accepted")
	}
}

func TestOperatorJournalCompactADRReferencesAreProjectBound(t *testing.T) {
	for _, adr := range []string{"ADR-legacy", "EXM-A1"} {
		if err := ValidateOperatorJournalReferencesForProject(OperatorJournalReferences{ADRs: []string{adr}}, "EXM"); err != nil {
			t.Fatalf("valid ADR %q rejected: %v", adr, err)
		}
	}
	for _, adr := range []string{"XYZ-A1", "EXM-A0", "EXM-A9007199254740992", "EXM-A1-extra"} {
		if err := ValidateOperatorJournalReferencesForProject(OperatorJournalReferences{ADRs: []string{adr}}, "EXM"); err == nil {
			t.Fatalf("invalid ADR %q accepted", adr)
		}
	}
	if err := ValidateOperatorJournalReferences(OperatorJournalReferences{ADRs: []string{"XYZ-A1"}}); err != nil {
		t.Fatalf("valid unbound compact ADR rejected: %v", err)
	}
}

func TestOperatorJournalSessionAndCorrectionSemantics(t *testing.T) {
	empty := ""
	event := testOperatorEvent(OperatorUserTalk, "EXM-O1")
	event.SessionID = &empty
	if err := ValidateOperatorJournalEvent(event); err == nil {
		t.Fatal("empty non-null session_id accepted")
	}
	event = testOperatorEvent(OperatorCorrection, "EXM-O2")
	if err := ValidateOperatorJournalEvent(event); err == nil {
		t.Fatal("correction without supersedes_event_id accepted")
	}
	event = testOperatorEvent(OperatorUserTalk, "EXM-O2")
	event.SupersedesEventID = "EXM-O1"
	if err := ValidateOperatorJournalEvent(event); err == nil {
		t.Fatal("non-correction with supersedes_event_id accepted")
	}
}

func TestOperatorJournalJSONSchemaDeclaresCorrectionAndADRParity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "operator-journal-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	session := properties["session_id"].(map[string]any)
	if session["minLength"] != float64(1) {
		t.Fatalf("session_id minLength=%v, want 1", session["minLength"])
	}
	allOf := schema["allOf"].([]any)
	if len(allOf) != 2 {
		t.Fatalf("allOf=%v, want correction bidirectional conditions", allOf)
	}
	defs := schema["$defs"].(map[string]any)
	adrIDs := defs["adr_ids"].(map[string]any)
	items := adrIDs["items"].(map[string]any)
	if len(items["anyOf"].([]any)) != 2 {
		t.Fatalf("ADR schema alternatives=%v, want legacy and compact", items["anyOf"])
	}
}

func mustOperatorJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
