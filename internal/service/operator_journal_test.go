package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	allocated := make([]string, 0, count)
	for id := range ids {
		allocated = append(allocated, id)
	}
	sort.Strings(allocated)
	want := []string{"EXM-O1", "EXM-O2", "EXM-O3", "EXM-O4"}
	if len(allocated) != len(want) {
		t.Fatalf("allocated %d event IDs, want %d", len(allocated), len(want))
	}
	for i := range want {
		if allocated[i] != want[i] {
			t.Fatalf("allocated IDs=%v, want=%v", allocated, want)
		}
	}
	history, err := s.OperatorHistory(ctx, OperatorHistoryInput{ProjectID: "example", Limit: count})
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if history.Events[i].ID != want[i] {
			t.Fatalf("history IDs=%v, want=%v", history.Events, want)
		}
	}
}

func operatorTestEvent(id string, projectID string) model.OperatorJournalEvent {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	return model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: id, ProjectID: projectID, SessionID: nil, Kind: model.OperatorUserTalk, Summary: "fixture", Content: operatorContent("fixture"), References: operatorReferences(), Actor: "owner", OccurredAt: now, RecordedAt: now}
}

func installOperatorEventFixture(t *testing.T, s *Service, revision, eventPath string, event model.OperatorJournalEvent, next uint64) string {
	t.Helper()
	counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: "example", NextEventNumber: next}
	eventBytes, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: install operator event fixture", func(worktree string) ([]string, error) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(worktree, filepath.FromSlash(eventPath))), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(worktree, filepath.FromSlash(eventPath)), eventBytes, 0o644); err != nil {
			return nil, err
		}
		counterPath := s.operatorCounterPath("example")
		if err := hub.WriteJSON(worktree, counterPath, counter); err != nil {
			return nil, err
		}
		return []string{eventPath, counterPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx.After
}

func TestOperatorRecordDoesNotOverwriteExistingEventOrAdvanceCounter(t *testing.T) {
	s, revision := operatorService(t)
	eventPath := s.operatorEventPath("example", "EXM-O1")
	original := operatorTestEvent("EXM-O1", "example")
	revision = installOperatorEventFixture(t, s, revision, eventPath, original, 1)
	eventBytes, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	beforeCounter, err := s.Hub.ReadFile(context.Background(), s.operatorCounterPath("example"))
	if err != nil {
		t.Fatal(err)
	}
	beforeRevision := revision
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "overwrite", Content: operatorContent("attempt"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
		t.Fatal("existing event target was overwritten")
	}
	afterRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || afterRevision != beforeRevision {
		t.Fatalf("failed append changed hub revision: before=%s after=%s err=%v", beforeRevision, afterRevision, err)
	}
	afterEvent, err := s.Hub.ReadFile(context.Background(), eventPath)
	if err != nil || string(afterEvent) != string(eventBytes) {
		t.Fatalf("existing event bytes changed: err=%v before=%s after=%s", err, eventBytes, afterEvent)
	}
	afterCounter, err := s.Hub.ReadFile(context.Background(), s.operatorCounterPath("example"))
	if err != nil || string(afterCounter) != string(beforeCounter) {
		t.Fatalf("counter changed: err=%v before=%s after=%s", err, beforeCounter, afterCounter)
	}
	cleanup, err := s.Hub.Transact(context.Background(), revision, "test: remove operator event fixture", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(eventPath))); err != nil {
			return nil, err
		}
		return []string{eventPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	valid, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "next", Content: operatorContent("valid"), References: operatorReferences(), Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: cleanup.After}})
	if err != nil || valid.ID != "EXM-O1" {
		t.Fatalf("next allocation changed after rejected overwrite: event=%#v err=%v", valid, err)
	}
}

func TestOperatorHistoryRejectsFilenameBodyMismatch(t *testing.T) {
	s, revision := operatorService(t)
	installOperatorEventFixture(t, s, revision, s.operatorEventPath("example", "EXM-O1"), operatorTestEvent("EXM-O2", "example"), 3)
	if _, err := s.OperatorHistory(context.Background(), OperatorHistoryInput{ProjectID: "example", Limit: 10}); err == nil {
		t.Fatal("history accepted filename/body event identity mismatch")
	}
}

func TestOperatorCorrectionRejectsFilenameBodyMismatch(t *testing.T) {
	s, revision := operatorService(t)
	installOperatorEventFixture(t, s, revision, s.operatorEventPath("example", "EXM-O1"), operatorTestEvent("EXM-O2", "example"), 2)
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{ProjectID: "example", Kind: model.OperatorCorrection, Summary: "correct", Content: operatorContent("correction"), References: operatorReferences(), SupersedesEventID: "EXM-O1", Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
		t.Fatal("correction accepted filename/body target mismatch")
	}
}

func TestOperatorReferencesBindCompactADRToProjectCode(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	for _, adr := range []string{"ADR-legacy", "EXM-A1"} {
		event, operation, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "adr", Content: operatorContent("reference"), References: model.OperatorJournalReferences{ADRs: []string{adr}}, Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
		if err != nil {
			t.Fatalf("ADR %q rejected: %v", adr, err)
		}
		revision = operation.Hub.After
		if event.References.ADRs[0] != adr {
			t.Fatalf("ADR reference changed: %#v", event.References.ADRs)
		}
	}
	for _, adr := range []string{"XYZ-A1", "EXM-A0", "EXM-A9007199254740992"} {
		if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{ProjectID: "example", Kind: model.OperatorUserTalk, Summary: "adr", Content: operatorContent("reference"), References: model.OperatorJournalReferences{ADRs: []string{adr}}, Actor: "owner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil {
			t.Fatalf("invalid ADR %q accepted", adr)
		}
	}
}
