package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) sharedOperatorEvents(ctx context.Context, projectID, projectCode string) ([]model.OperatorJournalEvent, error) {
	if s.Durability == nil {
		return nil, fmt.Errorf("Shared operator journal authority is unavailable")
	}
	entities, err := s.Durability.ListSharedEntities(ctx, "journal", 1000)
	if err != nil {
		return nil, err
	}
	items := make([]model.OperatorJournalEvent, 0, len(entities))
	seenIDs := map[string]bool{}
	seenNumbers := map[uint64]bool{}
	for _, entity := range entities {
		var event model.OperatorJournalEvent
		if err := decodeStrict(entity.Payload, &event); err != nil {
			return nil, fmt.Errorf("decode Shared operator event %s: %w", entity.ID, err)
		}
		if event.ProjectID != projectID || event.ID != entity.ID {
			return nil, fmt.Errorf("Shared operator event identity mismatch %q", entity.ID)
		}
		if err := model.ValidateOperatorJournalEvent(event); err != nil {
			return nil, err
		}
		if err := validateOperationalOperatorReferences(event.References, projectCode); err != nil {
			return nil, err
		}
		_, number, err := parseAnyOperatorEventIDForProject(event.ID, projectCode)
		if err != nil {
			return nil, err
		}
		if seenIDs[event.ID] || seenNumbers[number] {
			return nil, fmt.Errorf("duplicate Shared operator journal identity %q", event.ID)
		}
		seenIDs[event.ID], seenNumbers[number] = true, true
		items = append(items, event)
	}
	sort.Slice(items, func(i, j int) bool {
		_, left, _ := parseAnyOperatorEventIDForProject(items[i].ID, projectCode)
		_, right, _ := parseAnyOperatorEventIDForProject(items[j].ID, projectCode)
		return left < right
	})
	return items, nil
}

func (s *Service) validateSharedJournalSupersession(ctx context.Context, projectID, projectCode, targetID string) error {
	if err := model.ValidateOperatorEventIDForProject(targetID, projectCode); err != nil {
		return fmt.Errorf("supersedes_event_id: %w", err)
	}
	events, err := s.sharedOperatorEvents(ctx, projectID, projectCode)
	if err != nil {
		return err
	}
	found := false
	for _, event := range events {
		if event.ID == targetID {
			found = true
		}
		if event.SupersedesEventID == targetID {
			return fmt.Errorf("operator journal event %q is already superseded", targetID)
		}
	}
	if !found {
		return fmt.Errorf("supersedes_event_id target %q: %w", targetID, os.ErrNotExist)
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
		if err := validateAnyOperatorEventIDForProject(in.AfterEventID, identifiers.ProjectCode); err != nil {
			return OperatorHistoryResult{}, fmt.Errorf("after_event_id: %w", err)
		}
		_, afterNumber, _ = parseAnyOperatorEventIDForProject(in.AfterEventID, identifiers.ProjectCode)
	}
	if in.Kind != "" {
		if err := model.ValidateOperatorJournalKind(in.Kind); err != nil {
			return OperatorHistoryResult{}, err
		}
	}
	items, err := s.sharedOperatorEvents(ctx, in.ProjectID, identifiers.ProjectCode)
	if err != nil {
		return OperatorHistoryResult{}, err
	}
	filtered := make([]model.OperatorJournalEvent, 0, len(items))
	for _, event := range items {
		_, number, _ := parseAnyOperatorEventIDForProject(event.ID, identifiers.ProjectCode)
		if number <= afterNumber || (in.Kind != "" && event.Kind != in.Kind) {
			continue
		}
		filtered = append(filtered, event)
	}
	hasMore := len(filtered) > in.Limit
	if hasMore {
		filtered = filtered[:in.Limit]
	}
	next := ""
	if hasMore && len(filtered) > 0 {
		next = filtered[len(filtered)-1].ID
	}
	revision := ""
	if marker, markerErr := s.Durability.ReadSharedBootstrapMarker(ctx, in.ProjectID); markerErr == nil {
		revision = marker.HubRevision
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return OperatorHistoryResult{}, markerErr
	}
	return OperatorHistoryResult{ProjectID: in.ProjectID, Events: filtered, HubRevision: revision, HasMore: hasMore, NextAfterEventID: next}, nil
}
