package service

import (
	"context"
	"strings"
	"sync"
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
	s, revision, _ := testService(t)
	identifiers, operation, err := s.ProjectIdentifiersAdopt(context.Background(), ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil || identifiers.ProjectCode != "EXM" {
		t.Fatalf("adopt identifiers: %#v %v", identifiers, err)
	}
	return s, operation.Hub.After
}

func TestOperatorRecordHistoryCheckpointAndNumericPagination(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	first, firstOp, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "first", Content: operatorContent("one"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil || first.ID != "EXM-O1" || firstOp.Status != "recorded" {
		t.Fatalf("first record: %#v %#v %v", first, firstOp, err)
	}
	second, secondOp, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorTaskPlan, Summary: "second", Content: operatorContent("two"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: firstOp.Hub.After}})
	if err != nil || second.ID != "EXM-O2" {
		t.Fatalf("second record: %#v %v", second, err)
	}
	third, thirdOp, err := s.OperatorCheckpoint(ctx, OperatorCheckpointInput{ProjectID: "example", Summary: "checkpoint", Content: operatorContent("three"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: secondOp.Hub.After}})
	if err != nil || third.ID != "EXM-O3" || third.Kind != model.OperatorCheckpoint || thirdOp.Status != "checkpointed" {
		t.Fatalf("checkpoint: %#v %#v %v", third, thirdOp, err)
	}
	page, err := s.OperatorHistory(ctx, OperatorHistoryInput{ProjectID: "example", Limit: 2})
	if err != nil || len(page.Events) != 2 || page.Events[0].ID != "EXM-O1" || page.Events[1].ID != "EXM-O2" || !page.HasMore || page.NextAfterEventID != "EXM-O2" {
		t.Fatalf("unexpected first history page: %#v %v", page, err)
	}
	after, err := s.OperatorHistory(ctx, OperatorHistoryInput{ProjectID: "example", AfterEventID: page.NextAfterEventID, Kind: model.OperatorCheckpoint, Limit: 10})
	if err != nil || len(after.Events) != 1 || after.Events[0].ID != "EXM-O3" {
		t.Fatalf("unexpected filtered history page: %#v %v", after, err)
	}
	if _, err := s.OperatorHistory(ctx, OperatorHistoryInput{ProjectID: "example", AfterEventID: "OTHER-O1"}); err == nil {
		t.Fatal("cross-project history cursor accepted")
	}
	paths, err := s.Hub.List(ctx, s.operatorEventsPrefix("example"), ".json")
	if err != nil || len(paths) != 3 {
		t.Fatalf("unexpected immutable event paths: %#v %v", paths, err)
	}
}

func TestOperatorRecordReservedKindsMissingIdentifiersAndNoOpFailClosed(t *testing.T) {
	s, revision, _ := testService(t)
	ctx := context.Background()
	for _, kind := range []model.OperatorJournalKind{model.OperatorOperation, model.OperatorCheckpoint} {
		if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: kind, Summary: "reserved", Content: operatorContent("no"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
			t.Fatalf("reserved kind %q accepted", kind)
		}
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "empty", Content: model.OperatorJournalContent{}, References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
		t.Fatal("no-op record accepted")
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "missing identifiers", Content: operatorContent("fact"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
		t.Fatal("missing identifiers unexpectedly accepted")
	}
}

func TestOperatorCorrectionAndStaleRevisionFailClosed(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	first, op, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorTaskReview, Summary: "review", Content: operatorContent("reviewed"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	correction, correctionOp, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorCorrection, Summary: "corrected review", Content: operatorContent("corrected"), References: operatorReferences(), SupersedesEventID: first.ID, Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: op.Hub.After}})
	if err != nil || correction.ID != "EXM-O2" || correction.SupersedesEventID != first.ID {
		t.Fatalf("correction failed: %#v %v", correction, err)
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorCorrection, Summary: "duplicate correction", Content: operatorContent("duplicate"), References: operatorReferences(), SupersedesEventID: first.ID, Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: correctionOp.Hub.After}}); err == nil {
		t.Fatal("already superseded target accepted")
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "stale", Content: operatorContent("stale"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: strings.Repeat("0", 40)}}); err == nil {
		t.Fatal("stale explicit revision accepted")
	}
	after, err := s.Hub.RemoteRevision(ctx)
	if err != nil || before != after {
		t.Fatalf("stale revision mutated hub: before=%s after=%s err=%v", before, after, err)
	}
}

func TestOperatorConcurrentUnpinnedRecordsAllocateUniqueOrderedIDs(t *testing.T) {
	s, _ := operatorService(t)
	ctx := context.Background()
	const count = 4
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorReasoningSummary, Summary: "concurrent", Content: operatorContent("record"), References: operatorReferences(), Actor: "owner"})
			if err != nil {
				errs <- err
				return
			}
			ids <- event.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate event ID %s", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("allocated %d unique event IDs, want %d", len(seen), count)
	}
}
