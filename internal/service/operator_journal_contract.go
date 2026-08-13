package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateAnyOperatorEventID(eventID) != nil {
		return "../invalid-operator-event"
	}
	return s.operatorEventsPrefix(projectID) + "/" + eventID + ".json"
}

func (s *Service) operatorHistoryEventPath(projectID, eventID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil || model.ValidateAnyOperatorEventID(eventID) != nil {
		return "../invalid-operator-event"
	}
	return s.operatorEventsPrefix(projectID) + "/" + eventID + ".json"
}

func validateOperationalOperatorReferences(references model.OperatorJournalReferences, projectCode string) error {
	if err := model.ValidateOperatorJournalReferencesForProject(references, projectCode); err != nil {
		return err
	}
	for _, adr := range references.ADRs {
		if strings.HasPrefix(adr, "ADR-") || model.ValidateCanonicalADRIdentifier(adr) != nil {
			return fmt.Errorf("adrs: historical ADR identifiers are read-only")
		}
	}
	return nil
}

func parseAnyOperatorEventIDForProject(value, projectCode string) (string, uint64, error) {
	code, number, err := model.ParseAnyJournalEventID(value)
	if err != nil || code != projectCode {
		return "", 0, fmt.Errorf("operator event ID does not belong to project %q", projectCode)
	}
	return code, number, nil
}

func validateAnyOperatorEventIDForProject(value, projectCode string) error {
	_, _, err := parseAnyOperatorEventIDForProject(value, projectCode)
	return err
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
	_, number, err := model.ParseAnyJournalEventID(event.ID)
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

// appendOperatorEventInWorktree appends one bounded operator event and its
// counter update to an already-running Hub transaction. Callers that mutate
// another durable record must use this helper inside the same transaction.
