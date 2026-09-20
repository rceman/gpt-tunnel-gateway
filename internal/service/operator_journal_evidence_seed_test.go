package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type operatorEvidenceSeed struct {
	projectID         string
	sessionID         *string
	kind              model.OperatorJournalKind
	summary           string
	content           model.OperatorJournalContent
	references        model.OperatorJournalReferences
	supersedesEventID string
	actor             string
}

func tsk566SeedOperatorEvent(t *testing.T, s *Service, in operatorEvidenceSeed) model.OperatorJournalEvent {
	t.Helper()
	var event model.OperatorJournalEvent
	_, err := s.Hub.Transact(context.Background(), "", "test: seed operator journal evidence", func(worktree string) ([]string, error) {
		var identifiers model.ProjectIdentifiers
		if err := readWorktreeJSON(worktree, s.projectIdentifiersPath(in.projectID), &identifiers); err != nil {
			return nil, err
		}
		counterPath := s.projectPrefix(in.projectID) + "/operator-journal/counter.json"
		counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: in.projectID, NextEventNumber: 1}
		if data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(counterPath))); err == nil {
			if err := decodeStrict(data, &counter); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		number := counter.NextEventNumber
		eventID, err := model.FormatJournalID(identifiers.ProjectCode, number)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		event = model.OperatorJournalEvent{
			SchemaVersion: model.OperatorJournalSchemaVersion, ID: eventID, ProjectID: in.projectID,
			SessionID: in.sessionID, Kind: in.kind, Summary: in.summary, Content: in.content,
			References: in.references, SupersedesEventID: in.supersedesEventID, Actor: in.actor,
			OccurredAt: now, RecordedAt: now,
		}
		if err := model.ValidateOperatorJournalEvent(event); err != nil {
			return nil, err
		}
		counter.NextEventNumber = number + 1
		if err := model.ValidateOperatorJournalCounter(counter); err != nil {
			return nil, err
		}
		eventPath := s.operatorEventPath(in.projectID, event.ID)
		if err := hub.WriteJSON(worktree, eventPath, event); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, counterPath, counter); err != nil {
			return nil, err
		}
		return []string{counterPath, eventPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
