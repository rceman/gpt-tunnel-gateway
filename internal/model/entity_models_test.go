package model

import (
	"testing"
	"time"
)

func TestCanonicalEntityIDsAreProjectScopedAndBounded(t *testing.T) {
	for _, tc := range []struct {
		family string
		makeID func(string, uint64) (string, error)
		parse  func(string) (string, uint64, error)
		want   string
	}{
		{"rule", FormatRuleID, ParseRuleID, "EXM-RUL7"},
		{"message", FormatMessageID, ParseMessageID, "EXM-MSG7"},
		{"journal", FormatJournalID, ParseJournalID, "EXM-JRN7"},
	} {
		id, err := tc.makeID("EXM", 7)
		if err != nil || id != tc.want {
			t.Fatalf("%s id=%q err=%v", tc.family, id, err)
		}
		code, number, err := tc.parse(id)
		if err != nil || code != "EXM" || number != 7 {
			t.Fatalf("%s parse=%q/%d err=%v", tc.family, code, number, err)
		}
		if _, _, err := tc.parse("OTHER-" + id[4:]); err == nil {
			t.Fatalf("%s accepted wrong project", tc.family)
		}
		if _, err := tc.makeID("EXM", MaxSafeInteger+1); err == nil {
			t.Fatalf("%s accepted exhausted number", tc.family)
		}
	}
}

func TestRuleAndMessageValidationIsBounded(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	rule := Rule{
		SchemaVersion: SchemaVersion,
		ID:            "EXM-RUL1",
		ProjectID:     "example",
		Name:          "gate",
		Description:   "bounded",
		Enabled:       true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatal(err)
	}
	message := Message{
		SchemaVersion: SchemaVersion,
		ID:            "EXM-MSG1",
		ProjectID:     "example",
		Role:          "agent",
		Content:       "hello",
		CreatedAt:     now,
	}
	if err := ValidateMessage(message); err != nil {
		t.Fatal(err)
	}
	message.Content = string(make([]byte, MaxMessageTextBytes+1))
	if err := ValidateMessage(message); err == nil {
		t.Fatal("oversized message accepted")
	}
}
