package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) operatorRecord(ctx context.Context, in OperatorRecordInput, allowCheckpoint bool) (model.OperatorJournalEvent, OperationResult, error) {
	if err := validateOperatorInput(in.ProjectID, in.SessionID, in.Kind, in.Summary, in.Content, in.References, in.SupersedesEventID, in.Actor, allowCheckpoint); err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	occurredAt, err := parseOperatorOccurredAt(in.OccurredAt)
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	var event model.OperatorJournalEvent
	tx, err := s.operatorTransact(ctx, in.ExpectedHubRevision, "gateway: record operator journal event for "+in.ProjectID, func(worktree string) ([]string, error) {
		var project model.Project
		if err := readWorktreeJSON(worktree, s.projectPath(in.ProjectID), &project); err != nil {
			return nil, fmt.Errorf("project %q is not durable: %w", in.ProjectID, err)
		}
		if err := model.ValidateProject(project); err != nil {
			return nil, fmt.Errorf("project %q is invalid: %w", in.ProjectID, err)
		}
		if project.ID != in.ProjectID {
			return nil, fmt.Errorf("project %q has mismatched durable ID", in.ProjectID)
		}
		var identifiers model.ProjectIdentifiers
		if err := readWorktreeJSON(worktree, s.projectIdentifiersPath(in.ProjectID), &identifiers); err != nil {
			return nil, fmt.Errorf("project identifiers for %q: %w", in.ProjectID, err)
		}
		if err := model.ValidateProjectIdentifiers(identifiers); err != nil {
			return nil, fmt.Errorf("project identifiers for %q: %w", in.ProjectID, err)
		}
		if identifiers.ProjectID != in.ProjectID {
			return nil, fmt.Errorf("project identifiers project_id mismatch")
		}
		if err := validateOperationalOperatorReferences(in.References, identifiers.ProjectCode); err != nil {
			return nil, err
		}
		counterPath := s.operatorCounterPath(in.ProjectID)
		counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: in.ProjectID, NextEventNumber: 1}
		counterData, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(counterPath)))
		if readErr == nil {
			if err := decodeStrict(counterData, &counter); err != nil {
				return nil, fmt.Errorf("operator journal counter: %w", err)
			}
		} else if errors.Is(readErr, os.ErrNotExist) {
			if _, err := os.Stat(filepath.Join(worktree, filepath.FromSlash(s.operatorEventsPrefix(in.ProjectID)))); err == nil {
				return nil, fmt.Errorf("operator journal counter is missing while event history exists")
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else {
			return nil, readErr
		}
		if err := model.ValidateOperatorJournalCounter(counter); err != nil {
			return nil, err
		}
		if counter.ProjectID != in.ProjectID {
			return nil, fmt.Errorf("operator journal counter project_id mismatch")
		}
		number := counter.NextEventNumber
		if number > model.MaxSafeInteger {
			return nil, fmt.Errorf("operator journal counter exhausted")
		}
		eventID, err := model.FormatOperatorEventID(identifiers.ProjectCode, number)
		if err != nil {
			return nil, err
		}
		if in.SupersedesEventID != "" {
			if err := model.ValidateOperatorEventIDForProject(in.SupersedesEventID, identifiers.ProjectCode); err != nil {
				return nil, fmt.Errorf("supersedes_event_id: %w", err)
			}
			if in.SupersedesEventID == eventID {
				return nil, fmt.Errorf("operator journal event cannot supersede itself")
			}
			targetPath := s.operatorEventPath(in.ProjectID, in.SupersedesEventID)
			var target model.OperatorJournalEvent
			if err := readWorktreeJSON(worktree, targetPath, &target); err != nil {
				return nil, fmt.Errorf("supersedes_event_id target: %w", err)
			}
			if _, err := validateOperatorEventPathIdentity(targetPath, s.operatorEventsPrefix(in.ProjectID), target, in.ProjectID, identifiers.ProjectCode); err != nil {
				return nil, fmt.Errorf("supersedes_event_id target: %w", err)
			}
			if err := rejectAlreadySuperseded(worktree, s.operatorEventsPrefix(in.ProjectID), in.ProjectID, identifiers.ProjectCode, in.SupersedesEventID); err != nil {
				return nil, err
			}
		}
		recordedAt := time.Now().UTC()
		if occurredAt.IsZero() {
			occurredAt = recordedAt
		}
		if occurredAt.After(recordedAt) {
			return nil, fmt.Errorf("occurred_at cannot be after recorded_at")
		}
		event = model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: eventID, ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: in.Kind, Summary: in.Summary, Content: in.Content, References: in.References, SupersedesEventID: in.SupersedesEventID, Actor: in.Actor, OccurredAt: occurredAt, RecordedAt: recordedAt}
		if err := model.ValidateOperatorJournalEvent(event); err != nil {
			return nil, err
		}
		if err := validateOperationalOperatorReferences(event.References, identifiers.ProjectCode); err != nil {
			return nil, err
		}
		if number < model.MaxSafeInteger {
			counter.NextEventNumber = number + 1
		}
		if err := model.ValidateOperatorJournalCounter(counter); err != nil {
			return nil, err
		}
		eventPath := s.operatorEventPath(in.ProjectID, event.ID)
		if err := ensureOperatorEventPathAbsent(worktree, eventPath, event.ID); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, eventPath, event); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, counterPath, counter); err != nil {
			return nil, err
		}
		return []string{counterPath, eventPath}, nil
	})
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	return event, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "recorded",
	}, nil
}

func (s *Service) OperatorRecord(ctx context.Context, in OperatorRecordInput) (model.OperatorJournalEvent, OperationResult, error) {
	return s.operatorRecord(ctx, in, false)
}

func (s *Service) OperatorCheckpoint(ctx context.Context, in OperatorCheckpointInput) (model.OperatorJournalEvent, OperationResult, error) {
	event, operation, err := s.operatorRecord(ctx, OperatorRecordInput{
		ProjectID:    in.ProjectID,
		SessionID:    in.SessionID,
		Kind:         model.OperatorCheckpoint,
		Summary:      in.Summary,
		Content:      in.Content,
		References:   in.References,
		OccurredAt:   in.OccurredAt,
		Actor:        in.Actor,
		WriteOptions: in.WriteOptions,
	}, true)
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	operation.Status = "checkpointed"
	return event, operation, nil
}
