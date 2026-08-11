package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type WatcherGuideUpdateInput struct {
	ProjectID           string             `json:"project_id"`
	Guide               model.WatcherGuide `json:"guide"`
	ExpectedHubRevision string             `json:"expected_hub_revision,omitempty"`
}

// CanonicalWatcherGuideContent is the single behavioral guide payload. The
// effective copy lives in Hub at the canonical watcher guide path; this
// factory only provides revision-one content for explicit owner-authorized
// seeding or repair and never creates a second guide source.
const CanonicalWatcherGuideContent = "Perform one bounded watcher tick at a time.\n" +
	"When the active Task or Run identity changes, read the new Task once and reset observation state.\n" +
	"An empty unseen delta is non-terminal; a terminal Run is authoritative and must never be nudged.\n" +
	"Do not interrupt healthy investigation, tests, or useful progress.\n" +
	"Capacity or busy status alone is not failure.\n" +
	"Send at most one concise nudge for one unchanged evidence window or cooldown interval, and only to the explicitly idle watcher Agent.\n" +
	"Do not create or mutate Tasks, ADRs, dispatches, merges, releases, worktrees, or target Run state from watcher supervision.\n" +
	"Escalate architecture or scope ambiguity to Planner."

func CanonicalWatcherGuide(projectID, updatedBy string, updatedAt time.Time) model.WatcherGuide {
	return model.WatcherGuide{
		SchemaVersion: model.WatcherGuideSchemaVersion,
		ProjectID:     projectID,
		Revision:      1,
		Content:       CanonicalWatcherGuideContent,
		UpdatedBy:     updatedBy,
		UpdatedAt:     updatedAt,
	}
}

func (s *Service) watcherGuidePath(projectID string) string {
	return s.projectPrefix(projectID) + "/watcher/guide.json"
}

func (s *Service) WatcherGuideRead(ctx context.Context, projectID string) (model.WatcherGuide, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.WatcherGuide{}, err
	}
	var guide model.WatcherGuide
	if err := s.Hub.ReadJSON(ctx, s.watcherGuidePath(projectID), &guide); err != nil {
		return model.WatcherGuide{}, err
	}
	if err := model.ValidateWatcherGuide(guide); err != nil {
		return model.WatcherGuide{}, err
	}
	if guide.ProjectID != projectID {
		return model.WatcherGuide{}, fmt.Errorf("watcher guide project mismatch")
	}
	return guide, nil
}

func (s *Service) WatcherGuideUpdate(ctx context.Context, in WatcherGuideUpdateInput) (OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.Guide.ProjectID != in.ProjectID {
		return OperationResult{}, fmt.Errorf("watcher guide project mismatch")
	}
	if err := model.ValidateWatcherGuide(in.Guide); err != nil {
		return OperationResult{}, err
	}
	if in.ExpectedHubRevision == "" {
		var err error
		in.ExpectedHubRevision, err = s.hubRevision(ctx)
		if err != nil {
			return OperationResult{}, err
		}
	}
	path := s.watcherGuidePath(in.ProjectID)
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: update watcher guide "+in.ProjectID, func(w string) ([]string, error) {
		var current model.WatcherGuide
		readErr := readWorktreeJSON(w, path, &current)
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		if readErr == nil {
			if err := model.ValidateWatcherGuide(current); err != nil {
				return nil, err
			}
			if current.ProjectID != in.ProjectID || in.Guide.Revision != current.Revision+1 {
				return nil, fmt.Errorf("WATCHER_GUIDE_REVISION_CONFLICT expected=%d actual=%d", current.Revision+1, in.Guide.Revision)
			}
		} else if in.Guide.Revision != 1 {
			return nil, fmt.Errorf("first watcher guide revision must be 1")
		}
		if err := hub.WriteJSON(w, path, in.Guide); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "updated",
	}, nil
}
