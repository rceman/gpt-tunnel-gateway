package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) appendOperatorEventInWorktree(worktree string, in OperatorRecordInput) (model.OperatorJournalEvent, []string, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	if err := model.ValidateOperatorJournalKind(in.Kind); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	now := time.Now().UTC()
	probe := model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: "AAA-OPR1", ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: in.Kind, Summary: in.Summary, Content: in.Content, References: in.References, SupersedesEventID: in.SupersedesEventID, Actor: in.Actor, OccurredAt: now, RecordedAt: now}
	if err := model.ValidateOperatorJournalEvent(probe); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	occurredAt, err := parseOperatorOccurredAt(in.OccurredAt)
	if err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	var project model.Project
	if err := readWorktreeJSON(worktree, s.projectPath(in.ProjectID), &project); err != nil {
		return model.OperatorJournalEvent{}, nil, fmt.Errorf("project %q is not durable: %w", in.ProjectID, err)
	}
	if err := model.ValidateProject(project); err != nil || project.ID != in.ProjectID {
		return model.OperatorJournalEvent{}, nil, fmt.Errorf("project %q is invalid", in.ProjectID)
	}
	var identifiers model.ProjectIdentifiers
	if err := readWorktreeJSON(worktree, s.projectIdentifiersPath(in.ProjectID), &identifiers); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	if err := model.ValidateProjectIdentifiers(identifiers); err != nil || identifiers.ProjectID != in.ProjectID {
		return model.OperatorJournalEvent{}, nil, fmt.Errorf("project identifiers are invalid")
	}
	if err := validateOperationalOperatorReferences(in.References, identifiers.ProjectCode); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	counterPath := s.operatorCounterPath(in.ProjectID)
	counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: in.ProjectID, NextEventNumber: 1}
	counterData, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(counterPath)))
	if readErr == nil {
		if err := decodeStrict(counterData, &counter); err != nil {
			return model.OperatorJournalEvent{}, nil, err
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		if _, err := os.Stat(filepath.Join(worktree, filepath.FromSlash(s.operatorEventsPrefix(in.ProjectID)))); err == nil {
			return model.OperatorJournalEvent{}, nil, fmt.Errorf("operator journal counter is missing while event history exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return model.OperatorJournalEvent{}, nil, err
		}
	} else {
		return model.OperatorJournalEvent{}, nil, readErr
	}
	if err := model.ValidateOperatorJournalCounter(counter); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	if counter.ProjectID != in.ProjectID || counter.NextEventNumber > model.MaxSafeInteger {
		return model.OperatorJournalEvent{}, nil, fmt.Errorf("operator journal counter is invalid or exhausted")
	}
	eventID, err := model.FormatOperatorEventID(identifiers.ProjectCode, counter.NextEventNumber)
	if err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	recordedAt := time.Now().UTC()
	if occurredAt.IsZero() {
		occurredAt = recordedAt
	}
	if occurredAt.After(recordedAt) {
		return model.OperatorJournalEvent{}, nil, fmt.Errorf("occurred_at cannot be after recorded_at")
	}
	event := model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: eventID, ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: in.Kind, Summary: in.Summary, Content: in.Content, References: in.References, SupersedesEventID: in.SupersedesEventID, Actor: in.Actor, OccurredAt: occurredAt, RecordedAt: recordedAt}
	if err := model.ValidateOperatorJournalEvent(event); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	eventPath := s.operatorEventPath(in.ProjectID, event.ID)
	if err := ensureOperatorEventPathAbsent(worktree, eventPath, event.ID); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	if counter.NextEventNumber < model.MaxSafeInteger {
		counter.NextEventNumber++
	}
	if err := model.ValidateOperatorJournalCounter(counter); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	if err := hub.WriteJSON(worktree, eventPath, event); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	if err := hub.WriteJSON(worktree, counterPath, counter); err != nil {
		return model.OperatorJournalEvent{}, nil, err
	}
	return event, []string{counterPath, eventPath}, nil
}

func allowedBootstrapOperatorKind(kind model.OperatorJournalKind) bool {
	switch kind {
	case model.OperatorUserTalk, model.OperatorReasoningSummary, model.OperatorTaskPlan, model.OperatorTaskReview, model.OperatorCorrection:
		return true
	default:
		return false
	}
}

func validateOperatorInput(projectID string, sessionID *string, kind model.OperatorJournalKind, summary string, content model.OperatorJournalContent, references model.OperatorJournalReferences, supersedes, actor string, allowCheckpoint bool) error {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return err
	}
	if !allowedBootstrapOperatorKind(kind) && !(allowCheckpoint && kind == model.OperatorCheckpoint) {
		return fmt.Errorf("operator_record kind %q is reserved or invalid", kind)
	}
	now := time.Now().UTC()
	probe := model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: "AAA-OPR1", ProjectID: projectID, SessionID: sessionID, Kind: kind, Summary: summary, Content: content, References: references, SupersedesEventID: supersedes, Actor: actor, OccurredAt: now, RecordedAt: now}
	return model.ValidateOperatorJournalEvent(probe)
}

func parseOperatorOccurredAt(value *string) (time.Time, error) {
	if value == nil {
		return time.Time{}, nil
	}
	if strings.TrimSpace(*value) != *value || *value == "" {
		return time.Time{}, fmt.Errorf("occurred_at must be a strict RFC3339 timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return time.Time{}, fmt.Errorf("occurred_at must be a strict RFC3339 timestamp")
	}
	return parsed.UTC(), nil
}
