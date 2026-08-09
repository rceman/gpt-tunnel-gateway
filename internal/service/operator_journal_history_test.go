package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestOperatorCorrectionRejectsFilenameBodyMismatch(t *testing.T) {
	s, revision := operatorService(t)
	installOperatorEventFixture(t, s, revision, s.operatorEventPath("example", "EXM-OPR1"), operatorTestEvent("EXM-OPR2", "example"), 2)
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:         "example",
		Kind:              model.OperatorCorrection,
		Summary:           "correct",
		Content:           operatorContent("correction"),
		References:        operatorReferences(),
		SupersedesEventID: "EXM-OPR1",
		Actor:             "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil {
		t.Fatal("correction accepted filename/body target mismatch")
	}
}

func TestOperatorReferencesBindCompactADRToProjectCode(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	for _, adr := range []string{"EXM-ADR1"} {
		event, operation, err := s.OperatorRecord(ctx, OperatorRecordInput{
			ProjectID:  "example",
			Kind:       model.OperatorUserTalk,
			Summary:    "adr",
			Content:    operatorContent("reference"),
			References: model.OperatorJournalReferences{ADRs: []string{adr}},
			Actor:      "owner",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: revision,
			},
		})
		if err != nil {
			t.Fatalf("ADR %q rejected: %v", adr, err)
		}
		revision = operation.Hub.After
		if event.References.ADRs[0] != adr {
			t.Fatalf("ADR reference changed: %#v", event.References.ADRs)
		}
	}
	for _, adr := range []string{"ADR-legacy", "EXM-A1", "XYZ-ADR1", "EXM-ADR0", "EXM-ADR9007199254740992"} {
		if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
			ProjectID:  "example",
			Kind:       model.OperatorUserTalk,
			Summary:    "adr",
			Content:    operatorContent("reference"),
			References: model.OperatorJournalReferences{ADRs: []string{adr}},
			Actor:      "owner",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: revision,
			},
		}); err == nil {
			t.Fatalf("invalid ADR %q accepted", adr)
		}
	}
}
