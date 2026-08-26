package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
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
	if s.Durability == nil {
		return model.WatcherGuide{}, fmt.Errorf("Shared watcher guide authority is unavailable")
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "watcher_guide", projectID)
	if err != nil {
		return model.WatcherGuide{}, err
	}
	var guide model.WatcherGuide
	if err := json.Unmarshal(entity.Payload, &guide); err != nil {
		return model.WatcherGuide{}, err
	}
	if int64(guide.Revision) != entity.Revision {
		return model.WatcherGuide{}, fmt.Errorf("watcher guide Shared revision mismatch")
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
	if s.Durability == nil {
		return OperationResult{}, fmt.Errorf("Shared watcher guide authority is unavailable")
	}
	entityID := in.ProjectID
	expected := int64(0)
	create := true
	if current, readErr := s.Durability.ReadSharedEntity(ctx, "watcher_guide", entityID); readErr == nil {
		var guide model.WatcherGuide
		if err := json.Unmarshal(current.Payload, &guide); err != nil {
			return OperationResult{}, err
		}
		if err := model.ValidateWatcherGuide(guide); err != nil {
			return OperationResult{}, err
		}
		expected = current.Revision
		create = false
		if in.Guide.Revision != guide.Revision+1 {
			return OperationResult{}, fmt.Errorf("WATCHER_GUIDE_REVISION_CONFLICT expected=%d actual=%d", guide.Revision+1, in.Guide.Revision)
		}
	} else if !IsNotFound(readErr) {
		return OperationResult{}, readErr
	} else if in.Guide.Revision != 1 {
		return OperationResult{}, fmt.Errorf("first watcher guide revision must be 1")
	}
	payload, err := json.Marshal(in.Guide)
	if err != nil {
		return OperationResult{}, err
	}
	operationID := durableMutationOperationID(ctx)
	if operationID == "" {
		digest := sha256.Sum256(payload)
		operationID = "watcher-guide-" + hex.EncodeToString(digest[:])
	}
	if _, err := s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{OperationID: operationID, EntityType: "watcher_guide", EntityID: entityID, ExpectedRevision: expected, Revision: expected + 1, Kind: "watcher-guide-update", Payload: payload, CreatedAt: in.Guide.UpdatedAt, Create: create}); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		OperationID: operationID,
		ProjectID:   in.ProjectID,
		Status:      "updated",
	}, nil
}
