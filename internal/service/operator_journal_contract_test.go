package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func operatorContent(text string) model.OperatorJournalContent {
	return model.OperatorJournalContent{Facts: []string{text}}
}

func operatorReferences() model.OperatorJournalReferences {
	return model.OperatorJournalReferences{}
}

func operatorService(t *testing.T) (*Service, string) {
	t.Helper()
	s, revision, _ := testServiceWithoutIdentifiers(t)
	identifiers, operation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{
		ProjectID:   "example",
		ProjectCode: "EXM",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil || identifiers.ProjectCode != "EXM" {
		t.Fatalf("adopt identifiers: %#v %v", identifiers, err)
	}
	return s, operation.Hub.After
}

func TestOperatorRecordHistoryCheckpointAndNumericPagination(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	first, firstOp, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "first",
		Content:    operatorContent("one"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil || first.ID != "EXM-JRN1" || firstOp.Status != "recorded" {
		t.Fatalf("first record: %#v %#v %v", first, firstOp, err)
	}
	second, secondOp, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorTaskPlan,
		Summary:    "second",
		Content:    operatorContent("two"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: firstOp.Hub.After,
		},
	})
	if err != nil || second.ID != "EXM-JRN2" {
		t.Fatalf("second record: %#v %v", second, err)
	}
	third, thirdOp, err := s.OperatorCheckpoint(ctx, OperatorCheckpointInput{
		ProjectID:  "example",
		Summary:    "checkpoint",
		Content:    operatorContent("three"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: secondOp.Hub.After,
		},
	})
	if err != nil || third.ID != "EXM-JRN3" || third.Kind != model.OperatorCheckpoint || thirdOp.Status != "checkpointed" {
		t.Fatalf("checkpoint: %#v %#v %v", third, thirdOp, err)
	}
	page, err := s.OperatorHistory(ctx, OperatorHistoryInput{
		ProjectID: "example",
		Limit:     2,
	})
	if err != nil || len(page.Events) != 2 || page.Events[0].ID != "EXM-JRN1" || page.Events[1].ID != "EXM-JRN2" || !page.HasMore || page.NextAfterEventID != "EXM-JRN2" {
		t.Fatalf("unexpected first history page: %#v %v", page, err)
	}
	after, err := s.OperatorHistory(ctx, OperatorHistoryInput{
		ProjectID:    "example",
		AfterEventID: page.NextAfterEventID,
		Kind:         model.OperatorCheckpoint,
		Limit:        10,
	})
	if err != nil || len(after.Events) != 1 || after.Events[0].ID != "EXM-JRN3" {
		t.Fatalf("unexpected filtered history page: %#v %v", after, err)
	}
	if _, err := s.OperatorHistory(ctx, OperatorHistoryInput{
		ProjectID:    "example",
		AfterEventID: "OTHER-OPR1",
	}); err == nil {
		t.Fatal("cross-project history cursor accepted")
	}
	paths, err := s.Hub.List(ctx, s.operatorEventsPrefix("example"), ".json")
	if err != nil || len(paths) != 3 {
		t.Fatalf("unexpected immutable event paths: %#v %v", paths, err)
	}
}

func TestOperatorHistoryKeepsCollidingJournalFamiliesAndPaginatesByExactID(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	revision = installOperatorEventFixture(t, s, revision, s.operatorEventPath("example", "EXM-JRN1"), operatorTestEvent("EXM-JRN1", "example"), 2)
	revision = installOperatorEventFixture(t, s, revision, s.operatorEventPath("example", "EXM-OPR1"), operatorTestEvent("EXM-OPR1", "example"), 2)

	first, err := s.OperatorHistory(ctx, OperatorHistoryInput{ProjectID: "example", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].ID != "EXM-JRN1" || !first.HasMore || first.NextAfterEventID != "EXM-JRN1" {
		t.Fatalf("unexpected first colliding-family page: %#v", first)
	}

	second, err := s.OperatorHistory(ctx, OperatorHistoryInput{ProjectID: "example", AfterEventID: first.NextAfterEventID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 1 || second.Events[0].ID != "EXM-OPR1" || second.HasMore || second.NextAfterEventID != "" {
		t.Fatalf("unexpected second colliding-family page: %#v", second)
	}
}

func TestOperatorRecordReservedKindsMissingIdentifiersAndNoOpFailClosed(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()
	for _, kind := range []model.OperatorJournalKind{model.OperatorOperation, model.OperatorCheckpoint} {
		if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
			ProjectID:  "example",
			Kind:       kind,
			Summary:    "reserved",
			Content:    operatorContent("no"),
			References: operatorReferences(),
			Actor:      "owner",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: revision,
			},
		}); err == nil {
			t.Fatalf("reserved kind %q accepted", kind)
		}
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "empty",
		Content:    model.OperatorJournalContent{},
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil {
		t.Fatal("no-op record accepted")
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "missing identifiers",
		Content:    operatorContent("fact"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil {
		t.Fatal("missing identifiers unexpectedly accepted")
	}
}
