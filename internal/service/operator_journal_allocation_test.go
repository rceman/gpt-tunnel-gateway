package service

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
