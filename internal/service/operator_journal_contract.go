package service

import (
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
