package persistence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestCanonicalReplicaEqualityAcceptsFormattedJournalReplay(t *testing.T) {
	event := model.OperatorJournalEvent{
		SchemaVersion: 1, ID: "EXM-JRN1", ProjectID: "example", Kind: model.OperatorCheckpoint,
		Summary: "checkpoint", Actor: "owner", OccurredAt: time.Unix(10, 0).UTC(), RecordedAt: time.Unix(11, 0).UTC(),
	}
	raw, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	equal, err := canonicalJSONEqual(raw, event)
	if err != nil || !equal {
		t.Fatalf("formatted journal replay equal=%v err=%v", equal, err)
	}
}

func TestCanonicalReplicaEqualityRejectsJournalDivergence(t *testing.T) {
	event := model.OperatorJournalEvent{SchemaVersion: 1, ID: "EXM-JRN1", ProjectID: "example", Kind: model.OperatorCheckpoint, Summary: "checkpoint"}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	event.Summary = "different"
	equal, err := canonicalJSONEqual(raw, event)
	if err != nil || equal {
		t.Fatalf("divergent journal replay equal=%v err=%v", equal, err)
	}
}

func TestCanonicalReplicaEqualityRejectsADRPartialDifference(t *testing.T) {
	expected := model.ADR{SchemaVersion: 1, ID: "EXM-ADR1", ProjectID: "example", Title: "Title", Status: "accepted", Decision: "keep"}
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	expected.Decision = "replace"
	equal, err := canonicalJSONEqual(raw, expected)
	if err != nil || equal {
		t.Fatalf("divergent ADR replay equal=%v err=%v", equal, err)
	}
}

func TestCanonicalReplicaEqualityRejectsAgentPartialDifference(t *testing.T) {
	expected := model.Agent{SchemaVersion: 1, ProjectID: "example", AgentID: "coder", Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningHigh, Capabilities: []string{"go"}}
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	expected.Capabilities = []string{"go", "sql"}
	equal, err := canonicalJSONEqual(raw, expected)
	if err != nil || equal {
		t.Fatalf("divergent Agent replay equal=%v err=%v", equal, err)
	}
}
