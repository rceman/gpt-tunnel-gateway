package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

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
	entityID := strings.TrimSuffix(filepath.Base(eventPath), ".json")
	updatedAt := event.RecordedAt.UTC().Format(time.RFC3339Nano)
	if event.RecordedAt.IsZero() {
		updatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := s.Durability.PutSharedProjection(context.Background(), "journal", sqlitestore.SharedEntity{ID: entityID, Revision: 1, Payload: eventBytes, UpdatedAt: updatedAt}); err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.PutSharedJournalSequence(context.Background(), "example", "EXM", int64(next)); err != nil {
		t.Fatal(err)
	}
	return tx.After
}

func TestOperatorRecordDoesNotOverwriteExistingEventOrAdvanceCounter(t *testing.T) {
	s, revision := operatorService(t)
	eventPath := s.operatorEventPath("example", "EXM-JRN1")
	original := operatorTestEvent("EXM-JRN1", "example")
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
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "next",
		Content:    operatorContent("valid"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: cleanup.After,
		},
	}); err == nil {
		t.Fatal("Shared journal overwrite unexpectedly became available after Hub-only cleanup")
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
	if err := s.Durability.PutSharedJournalSequence(context.Background(), "example", "EXM", int64(model.MaxSafeInteger)); err != nil {
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
	wantID, err := model.FormatJournalID("EXM", model.MaxSafeInteger)
	if err != nil || first.ID != wantID {
		t.Fatalf("first max allocation=%s want=%s err=%v", first.ID, wantID, err)
	}
	if _, _, err := s.OperatorRecord(context.Background(), OperatorRecordInput{
		ProjectID:  "example",
		Kind:       model.OperatorUserTalk,
		Summary:    "reuse",
		Content:    operatorContent("reuse"),
		References: operatorReferences(),
		Actor:      "owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err == nil {
		t.Fatal("max operator counter was reused")
	}
	if events, err := s.Durability.ListSharedEntities(context.Background(), "journal", 10); err != nil || len(events) != 1 {
		t.Fatalf("failed max reuse changed Shared journal: events=%d err=%v", len(events), err)
	}
	code, next, found, err := s.Durability.ReadSharedJournalSequence(context.Background(), "example")
	if err != nil || !found || code != "EXM" || uint64(next) != model.MaxSafeInteger {
		t.Fatalf("max Shared counter advanced after allocation: code=%s next=%d found=%v err=%v", code, next, found, err)
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
