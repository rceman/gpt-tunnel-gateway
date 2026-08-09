package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if err != nil || first.ID != "EXM-OPR1" || firstOp.Status != "recorded" {
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
	if err != nil || second.ID != "EXM-OPR2" {
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
	if err != nil || third.ID != "EXM-OPR3" || third.Kind != model.OperatorCheckpoint || thirdOp.Status != "checkpointed" {
		t.Fatalf("checkpoint: %#v %#v %v", third, thirdOp, err)
	}
	page, err := s.OperatorHistory(ctx, OperatorHistoryInput{
		ProjectID: "example",
		Limit:     2,
	})
	if err != nil || len(page.Events) != 2 || page.Events[0].ID != "EXM-OPR1" || page.Events[1].ID != "EXM-OPR2" || !page.HasMore || page.NextAfterEventID != "EXM-OPR2" {
		t.Fatalf("unexpected first history page: %#v %v", page, err)
	}
	after, err := s.OperatorHistory(ctx, OperatorHistoryInput{
		ProjectID:    "example",
		AfterEventID: page.NextAfterEventID,
		Kind:         model.OperatorCheckpoint,
		Limit:        10,
	})
	if err != nil || len(after.Events) != 1 || after.Events[0].ID != "EXM-OPR3" {
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

func TestOperatorCorrectionAndStaleRevisionFailClosed(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	first, op, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorTaskReview,
		Summary:    "review",
		Content:    operatorContent("reviewed"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	correction, correctionOp, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:         "example",
		Kind:              model.OperatorCorrection,
		Summary:           "corrected review",
		Content:           operatorContent("corrected"),
		References:        operatorReferences(),
		SupersedesEventID: first.ID,
		Actor:             "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: op.Hub.After,
		},
	})
	if err != nil || correction.ID != "EXM-OPR2" || correction.SupersedesEventID != first.ID {
		t.Fatalf("correction failed: %#v %v", correction, err)
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:         "example",
		Kind:              model.OperatorCorrection,
		Summary:           "duplicate correction",
		Content:           operatorContent("duplicate"),
		References:        operatorReferences(),
		SupersedesEventID: first.ID,
		Actor:             "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: correctionOp.Hub.After,
		},
	}); err == nil {
		t.Fatal("already superseded target accepted")
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "stale",
		Content:    operatorContent("stale"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: strings.Repeat("0", 40),
		},
	}); err == nil {
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
			event, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
				ProjectID:  "example",
				Kind:       model.OperatorReasoningSummary,
				Summary:    "concurrent",
				Content:    operatorContent("record"),
				References: operatorReferences(),
				Actor:      "owner",
			})
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
	want := []string{"EXM-OPR1", "EXM-OPR2", "EXM-OPR3", "EXM-OPR4"}
	if len(allocated) != len(want) {
		t.Fatalf("allocated %d event IDs, want %d", len(allocated), len(want))
	}
	for i := range want {
		if allocated[i] != want[i] {
			t.Fatalf("allocated IDs=%v, want=%v", allocated, want)
		}
	}
	history, err := s.OperatorHistory(ctx, OperatorHistoryInput{
		ProjectID: "example",
		Limit:     count,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if history.Events[i].ID != want[i] {
			t.Fatalf("history IDs=%v, want=%v", history.Events, want)
		}
	}
}

func TestOperatorConcurrentUnpinnedCorrectionsAllocateUniqueOrderedIDs(t *testing.T) {
	s, revision := operatorService(t)
	ctx := context.Background()
	targets := make([]string, 4)
	for i := range targets {
		event, operation, err := s.OperatorRecord(ctx, OperatorRecordInput{
			ProjectID:  "example",
			Kind:       model.OperatorTaskReview,
			Summary:    "review",
			Content:    operatorContent("review"),
			References: operatorReferences(),
			Actor:      "owner",
			WriteOptions: WriteOptions{
				ExpectedHubRevision: revision,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		targets[i], revision = event.ID, operation.Hub.After
	}
	ids := make(chan string, len(targets))
	errs := make(chan error, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			event, _, err := s.OperatorRecord(ctx, OperatorRecordInput{
				ProjectID:         "example",
				Kind:              model.OperatorCorrection,
				Summary:           "correction",
				Content:           operatorContent("correction"),
				References:        operatorReferences(),
				SupersedesEventID: target,
				Actor:             "owner",
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- event.ID
		}(target)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	allocated := make([]string, 0, len(targets))
	for id := range ids {
		allocated = append(allocated, id)
	}
	sort.Strings(allocated)
	want := []string{"EXM-OPR5", "EXM-OPR6", "EXM-OPR7", "EXM-OPR8"}
	if !reflect.DeepEqual(allocated, want) {
		t.Fatalf("allocated correction IDs=%v, want=%v", allocated, want)
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
	eventPath := s.operatorEventPath("example", "EXM-OPR1")
	original := operatorTestEvent("EXM-OPR1", "example")
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
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "overwrite",
		Content:    operatorContent("attempt"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil {
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
	valid, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "next",
		Content:    operatorContent("valid"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: cleanup.After,
		},
	})
	if err != nil || valid.ID != "EXM-OPR1" {
		t.Fatalf("next allocation changed after rejected overwrite: event=%#v err=%v", valid, err)
	}
}

func TestOperatorMaxCounterAllocatesOnceAndCannotReuse(t *testing.T) {
	s, revision := operatorService(t)
	counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: "example", NextEventNumber: model.MaxSafeInteger}
	counterBytes, err := json.Marshal(counter)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: install max operator counter", func(worktree string) ([]string, error) {
		path := filepath.Join(worktree, filepath.FromSlash(s.operatorCounterPath("example")))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, counterBytes, 0o644); err != nil {
			return nil, err
		}
		return []string{s.operatorCounterPath("example")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, operation, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "max",
		Content:    operatorContent("max"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantID, err := model.FormatOperatorEventID("EXM", model.MaxSafeInteger)
	if err != nil || first.ID != wantID {
		t.Fatalf("first max allocation=%s want=%s err=%v", first.ID, wantID, err)
	}
	beforeRetryRevision := operation.Hub.After
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "reuse",
		Content:    operatorContent("reuse"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: beforeRetryRevision,
		},
	}); err == nil {
		t.Fatal("max operator counter was reused")
	}
	afterRetryRevision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if afterRetryRevision != beforeRetryRevision {
		t.Fatalf("failed max reuse mutated hub: before=%s after=%s", beforeRetryRevision, afterRetryRevision)
	}
	var stored model.OperatorJournalCounter
	if err := s.Hub.ReadJSON(context.Background(), s.operatorCounterPath("example"), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.NextEventNumber != model.MaxSafeInteger {
		t.Fatalf("max counter advanced after allocation: %d", stored.NextEventNumber)
	}
}

func TestOperatorHistoryRejectsFilenameBodyMismatch(t *testing.T) {
	s, revision := operatorService(t)
	installOperatorEventFixture(t, s, revision, s.operatorEventPath("example", "EXM-OPR1"), operatorTestEvent("EXM-OPR2", "example"), 3)
	if _, err := s.OperatorHistory(context.Background(), OperatorHistoryInput{
		ProjectID: "example",
		Limit:     10,
	}); err == nil {
		t.Fatal("history accepted filename/body event identity mismatch")
	}
}

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
