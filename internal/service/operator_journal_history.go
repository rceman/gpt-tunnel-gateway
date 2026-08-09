package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
		_, left, _ := parseAnyOperatorEventIDForProject(items[i].ID, identifiers.ProjectCode)
		_, right, _ := parseAnyOperatorEventIDForProject(items[j].ID, identifiers.ProjectCode)
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
	return OperatorHistoryResult{
		ProjectID:        in.ProjectID,
		Events:           items,
		HubRevision:      revision,
		HasMore:          hasMore,
		NextAfterEventID: next,
	}, nil
}
