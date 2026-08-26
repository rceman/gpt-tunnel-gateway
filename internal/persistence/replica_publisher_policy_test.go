package persistence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestReplicaPolicyRejectsImmutableDivergence(t *testing.T) {
	adr := model.ADR{SchemaVersion: 1, ID: "GTW-ADR1", ProjectID: "gpt-tunnel-gateway", Title: "Title", Status: "accepted", Decision: "keep"}
	adrRaw, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := immutableReplicaReplay(adrRaw, adr, "Hub ADR"); err != nil || !equal {
		t.Fatalf("identical ADR was not equal: equal=%v err=%v", equal, err)
	}
	divergent := adr
	divergent.Decision = "replace"
	if equal, err := immutableReplicaReplay(adrRaw, divergent, "Hub ADR"); err == nil || equal {
		t.Fatalf("divergent ADR was not rejected: equal=%v err=%v", equal, err)
	}

	event := model.OperatorJournalEvent{SchemaVersion: 1, ID: "GTW-JRN1", ProjectID: "gpt-tunnel-gateway", Summary: "original", OccurredAt: time.Unix(1, 0).UTC(), RecordedAt: time.Unix(2, 0).UTC()}
	divergentEvent := event
	divergentEvent.Summary = "changed"
	eventRaw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := immutableReplicaReplay(eventRaw, divergentEvent, "Hub operator event"); err == nil || equal {
		t.Fatalf("divergent Journal was not rejected: equal=%v err=%v", equal, err)
	}
}

func TestReplicaPolicyAllowsOnlyOlderVersionToAdvance(t *testing.T) {
	old := model.Agent{SchemaVersion: 1, ProjectID: "gpt-tunnel-gateway", AgentID: "coder", Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningHigh, UpdatedAt: time.Unix(1, 0).UTC()}
	oldRaw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := versionedAgentReplay(oldRaw, old); err != nil || !equal {
		t.Fatalf("identical Agent replay was not reused: equal=%v err=%v", equal, err)
	}
	newer := old
	newer.UpdatedAt = time.Unix(2, 0).UTC()
	if advance, err := versionedAgentReplay(oldRaw, newer); err != nil || advance {
		t.Fatalf("older Agent replica was not allowed to advance: advance=%v err=%v", advance, err)
	}
	newerRaw, err := json.Marshal(newer)
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := versionedAgentReplay(newerRaw, old); err == nil || equal {
		t.Fatalf("newer Agent replica was not rejected: equal=%v err=%v", equal, err)
	}
	sameVersion := old
	sameVersion.Capabilities = []string{"different"}
	if equal, err := versionedAgentReplay(oldRaw, sameVersion); err == nil || equal {
		t.Fatalf("same-version Agent divergence was not rejected: equal=%v err=%v", equal, err)
	}
}
