package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type OperatorRecordInput struct {
	ProjectID         string                          `json:"project_id"`
	SessionID         *string                         `json:"session_id"`
	Kind              model.OperatorJournalKind       `json:"kind"`
	Summary           string                          `json:"summary"`
	Content           model.OperatorJournalContent    `json:"content"`
	References        model.OperatorJournalReferences `json:"references"`
	SupersedesEventID string                          `json:"supersedes_event_id,omitempty"`
	OccurredAt        *string                         `json:"occurred_at,omitempty"`
	Actor             string                          `json:"actor"`
	WriteOptions
}

type OperatorCheckpointInput struct {
	ProjectID  string                          `json:"project_id"`
	SessionID  *string                         `json:"session_id"`
	Summary    string                          `json:"summary"`
	Content    model.OperatorJournalContent    `json:"content"`
	References model.OperatorJournalReferences `json:"references"`
	OccurredAt *string                         `json:"occurred_at,omitempty"`
	Actor      string                          `json:"actor"`
	WriteOptions
}

type OperatorHistoryInput struct {
	ProjectID    string                    `json:"project_id"`
	AfterEventID string                    `json:"after_event_id,omitempty"`
	Kind         model.OperatorJournalKind `json:"kind,omitempty"`
	Limit        int                       `json:"limit,omitempty"`
}

type OperatorHistoryResult struct {
	ProjectID        string                       `json:"project_id"`
	Events           []model.OperatorJournalEvent `json:"events"`
	HubRevision      string                       `json:"hub_revision"`
	HasMore          bool                         `json:"has_more"`
	NextAfterEventID string                       `json:"next_after_event_id,omitempty"`
}

func (s *Service) operatorCounterPath(projectID string) string {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "../invalid-operator-counter"
	}
	return s.projectPrefix(projectID) + "/operator-journal/counter.json"
}

func (s *Service) operatorEventsPrefix(projectID string) string {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return "../invalid-operator-events"
	}
	return s.projectPrefix(projectID) + "/operator-journal/events"
}

func (s *Service) operatorEventPath(projectID, eventID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateOperatorEventID(eventID) != nil {
		return "../invalid-operator-event"
	}
	return s.operatorEventsPrefix(projectID) + "/" + eventID + ".json"
}

func validateOperatorEventPathIdentity(eventPath, eventsPrefix string, event model.OperatorJournalEvent, projectID, projectCode string) (uint64, error) {
	if err := model.ValidateOperatorJournalEvent(event); err != nil {
		return 0, err
	}
	if err := model.ValidateOperatorJournalReferencesForProject(event.References, projectCode); err != nil {
		return 0, err
	}
	if event.ProjectID != projectID {
		return 0, fmt.Errorf("operator event project mismatch")
	}
	if err := model.ValidateOperatorEventIDForProject(event.ID, projectCode); err != nil {
		return 0, err
	}
	prefix := strings.TrimSuffix(eventsPrefix, "/") + "/"
	if !strings.HasPrefix(eventPath, prefix) {
		return 0, fmt.Errorf("operator event path %q is outside event directory", eventPath)
	}
	relative := strings.TrimPrefix(eventPath, prefix)
	if relative == "" || strings.Contains(relative, "/") || !strings.HasSuffix(relative, ".json") {
		return 0, fmt.Errorf("invalid operator event path %q", eventPath)
	}
	pathID := strings.TrimSuffix(relative, ".json")
	if pathID != event.ID {
		return 0, fmt.Errorf("operator event path/body ID mismatch: path %q body %q", pathID, event.ID)
	}
	_, number, err := model.ParseOperatorEventID(event.ID)
	if err != nil {
		return 0, err
	}
	return number, nil
}

func ensureOperatorEventPathAbsent(worktree, eventPath, eventID string) error {
	_, err := os.Lstat(filepath.Join(worktree, filepath.FromSlash(eventPath)))
	if err == nil {
		return fmt.Errorf("operator journal event %q already exists", eventID)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect operator journal event %q: %w", eventID, err)
	}
	return nil
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
	probe := model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: "AAA-O1", ProjectID: projectID, SessionID: sessionID, Kind: kind, Summary: summary, Content: content, References: references, SupersedesEventID: supersedes, Actor: actor, OccurredAt: now, RecordedAt: now}
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

func isOperatorHubConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "hub_revision_conflict") || strings.Contains(message, "non-fast-forward") || strings.Contains(message, "fetch first") || strings.Contains(message, "resource temporarily unavailable")
}

func (s *Service) operatorTransact(ctx context.Context, expected, subject string, mutate hub.Mutator) (hub.TransactionResult, error) {
	attempts := 1
	if expected == "" {
		attempts = 20
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := s.Hub.Transact(ctx, expected, subject, mutate)
		if err == nil {
			return result, nil
		}
		last = err
		if expected != "" || !isOperatorHubConflict(err) || attempt+1 == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return hub.TransactionResult{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return hub.TransactionResult{}, last
}

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
		if err := model.ValidateOperatorJournalReferencesForProject(in.References, identifiers.ProjectCode); err != nil {
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
		if number >= model.MaxSafeInteger {
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
		if err := model.ValidateOperatorJournalReferencesForProject(event.References, identifiers.ProjectCode); err != nil {
			return nil, err
		}
		counter.NextEventNumber = number + 1
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
	return event, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: "recorded"}, nil
}

func (s *Service) OperatorRecord(ctx context.Context, in OperatorRecordInput) (model.OperatorJournalEvent, OperationResult, error) {
	return s.operatorRecord(ctx, in, false)
}

func (s *Service) OperatorCheckpoint(ctx context.Context, in OperatorCheckpointInput) (model.OperatorJournalEvent, OperationResult, error) {
	event, operation, err := s.operatorRecord(ctx, OperatorRecordInput{ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: model.OperatorCheckpoint, Summary: in.Summary, Content: in.Content, References: in.References, OccurredAt: in.OccurredAt, Actor: in.Actor, WriteOptions: in.WriteOptions}, true)
	if err != nil {
		return model.OperatorJournalEvent{}, OperationResult{}, err
	}
	operation.Status = "checkpointed"
	return event, operation, nil
}

func rejectAlreadySuperseded(worktree, eventsPrefix, projectID, projectCode, targetID string) error {
	root := filepath.Join(worktree, filepath.FromSlash(eventsPrefix))
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	seenIDs := map[string]bool{}
	seenNumbers := map[uint64]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		eventPath := eventsPrefix + "/" + entry.Name()
		var event model.OperatorJournalEvent
		if err := readWorktreeJSON(worktree, eventPath, &event); err != nil {
			return fmt.Errorf("read operator journal event %q: %w", entry.Name(), err)
		}
		number, err := validateOperatorEventPathIdentity(eventPath, eventsPrefix, event, projectID, projectCode)
		if err != nil {
			return fmt.Errorf("invalid operator journal event %q: %w", entry.Name(), err)
		}
		if seenIDs[event.ID] || seenNumbers[number] {
			return fmt.Errorf("duplicate operator journal event identity %q", event.ID)
		}
		seenIDs[event.ID] = true
		seenNumbers[number] = true
		if event.SupersedesEventID == targetID {
			return fmt.Errorf("operator journal event %q is already superseded", targetID)
		}
	}
	return nil
}

func (s *Service) OperatorHistory(ctx context.Context, in OperatorHistoryInput) (OperatorHistoryResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return OperatorHistoryResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return OperatorHistoryResult{}, err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > model.MaxOperatorHistoryLimit {
		return OperatorHistoryResult{}, fmt.Errorf("operator history limit must be between 1 and %d", model.MaxOperatorHistoryLimit)
	}
	var afterNumber uint64
	if in.AfterEventID != "" {
		if err := model.ValidateOperatorEventIDForProject(in.AfterEventID, identifiers.ProjectCode); err != nil {
			return OperatorHistoryResult{}, fmt.Errorf("after_event_id: %w", err)
		}
		_, afterNumber, _ = model.ParseOperatorEventID(in.AfterEventID)
	}
	if in.Kind != "" {
		if err := model.ValidateOperatorJournalKind(in.Kind); err != nil {
			return OperatorHistoryResult{}, err
		}
	}
	eventsPrefix := s.operatorEventsPrefix(in.ProjectID)
	paths, err := s.Hub.List(ctx, eventsPrefix, ".json")
	if err != nil {
		return OperatorHistoryResult{}, err
	}
	items := make([]model.OperatorJournalEvent, 0, len(paths))
	seenIDs := map[string]bool{}
	seenNumbers := map[uint64]bool{}
	for _, path := range paths {
		var event model.OperatorJournalEvent
		if err := s.Hub.ReadJSON(ctx, path, &event); err != nil {
			return OperatorHistoryResult{}, err
		}
		number, err := validateOperatorEventPathIdentity(path, eventsPrefix, event, in.ProjectID, identifiers.ProjectCode)
		if err != nil {
			return OperatorHistoryResult{}, fmt.Errorf("invalid operator event %s: %w", path, err)
		}
		if seenIDs[event.ID] || seenNumbers[number] {
			return OperatorHistoryResult{}, fmt.Errorf("duplicate operator journal event identity %q", event.ID)
		}
		seenIDs[event.ID] = true
		seenNumbers[number] = true
		if number <= afterNumber || (in.Kind != "" && event.Kind != in.Kind) {
			continue
		}
		items = append(items, event)
	}
	sort.Slice(items, func(i, j int) bool {
		_, left, _ := model.ParseOperatorEventID(items[i].ID)
		_, right, _ := model.ParseOperatorEventID(items[j].ID)
		return left < right
	})
	hasMore := len(items) > in.Limit
	if hasMore {
		items = items[:in.Limit]
	}
	next := ""
	if hasMore && len(items) > 0 {
		next = items[len(items)-1].ID
	}
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		return OperatorHistoryResult{}, err
	}
	return OperatorHistoryResult{ProjectID: in.ProjectID, Events: items, HubRevision: revision, HasMore: hasMore, NextAfterEventID: next}, nil
}
