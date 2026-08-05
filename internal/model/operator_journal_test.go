package model

import (
	"encoding/json"
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

func mustOperatorJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
