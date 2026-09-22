package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type TrackTaskProjection struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Priority     string   `json:"priority,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Status       string   `json:"status"`
	Revision     int      `json:"revision"`
}

type TrackView struct {
	Track model.Track
	Tasks []TrackTaskProjection
}

type TrackPage struct {
	Tracks     []TrackView
	NextCursor string
	HasMore    bool
	CursorKind string
}

type TrackCreateInput struct {
	ProjectID string
	Milestone string
	Title     string
	Summary   string
	Tasks     []string
	CreatedBy string
}

type TrackUpdateInput struct {
	ProjectID string
	Key       string
	Title     *string
	Summary   *string
	Tasks     *[]string
	Actor     string
	Reason    string
}

type TrackMembershipInput struct {
	ProjectID string
	Key       string
	Tasks     []string
	Actor     string
	Reason    string
}

type TrackCancelInput struct {
	ProjectID string
	Key       string
	Actor     string
	Reason    string
}

type TrackReviewResult struct {
	Track model.Track
	View  TrackView
}

func (s *Service) TrackLifecycleCreate(ctx context.Context, in TrackCreateInput, operationID string) (model.Track, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if in.CreatedBy == "" {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track creator is required")
	}
	milestone, err := s.MilestoneLifecycleRead(ctx, in.ProjectID, in.Milestone, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if milestone.Status == model.MilestoneCompleted || milestone.Status == model.MilestoneArchived {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track cannot be created for terminal Milestone %q", in.Milestone)
	}
	code, err := s.sharedTaskProjectCode(ctx, in.ProjectID)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if err := s.validateTrackTasks(ctx, in.ProjectID, code, milestone, in.Tasks); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	for _, taskID := range in.Tasks {
		if other, found, findErr := s.nonterminalTrackForTaskExcluding(ctx, in.ProjectID, taskID, ""); findErr != nil {
			return model.Track{}, OperationResult{}, findErr
		} else if found {
			return model.Track{}, OperationResult{}, fmt.Errorf("Task %q already belongs to nonterminal Track %q", taskID, other.ID)
		}
	}
	if operationID == "" {
		digest, marshalErr := json.Marshal(in)
		if marshalErr != nil {
			return model.Track{}, OperationResult{}, marshalErr
		}
		hash := sha256.Sum256(digest)
		operationID = "track-create-" + hex.EncodeToString(hash[:])
	}
	now := s.durableNow().UTC()
	var created model.Track
	_, _, payload, err := s.Durability.CommitSharedLifecycleCreate(ctx, sqlitestore.SharedLifecycleCreate{
		OperationID:         operationID,
		EntityType:          "track",
		ProjectID:           in.ProjectID,
		ProjectCode:         code,
		InitialNextNumber:   1,
		Kind:                "track-create",
		HistoryMutationKind: "create",
		Actor:               in.CreatedBy,
		Reason:              "create",
		ChangedFields:       []string{"milestone", "title", "summary", "tasks"},
		CreatedAt:           now,
		BuildPayload: func(id string) ([]byte, error) {
			var createErr error
			created, createErr = model.NewTrack(in.ProjectID, id, in.Milestone, in.Title, in.Summary, in.Tasks, in.CreatedBy, now)
			if createErr != nil {
				return nil, createErr
			}
			return json.Marshal(created)
		},
	})
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if err := json.Unmarshal(payload, &created); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	return created, OperationResult{
		OperationID: operationID,
		ProjectID:   created.ProjectID,
		EntityKey:   created.ID,
		Revision:    created.Revision,
		Status:      created.Status,
	}, nil
}

func (s *Service) TrackLifecycleRead(ctx context.Context, projectID, key string, revision int) (model.Track, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.Track{}, err
	}
	stored, err := s.trackReadStored(ctx, projectID, key, revision)
	if err != nil || revision != 0 {
		return stored, err
	}
	status, err := s.deriveTrackStatus(ctx, stored)
	if err != nil {
		return model.Track{}, err
	}
	stored.Status = status
	return stored, nil
}

func (s *Service) trackReadStored(ctx context.Context, projectID, key string, revision int) (model.Track, error) {
	if revision < 0 {
		return model.Track{}, fmt.Errorf("invalid Track revision")
	}
	var payload []byte
	if revision == 0 {
		entity, err := s.Durability.ReadSharedEntity(ctx, "track", key)
		if err != nil {
			return model.Track{}, err
		}
		payload = entity.Payload
	} else {
		record, err := s.Durability.ReadSharedRevision(ctx, "track", projectID, key, int64(revision))
		if err != nil {
			return model.Track{}, err
		}
		payload = record.Payload
	}
	var track model.Track
	if err := json.Unmarshal(payload, &track); err != nil {
		return model.Track{}, fmt.Errorf("decode shared Track %s: %w", key, err)
	}
	if track.ID != key || track.ProjectID != projectID {
		return model.Track{}, fmt.Errorf("shared Track ownership mismatch")
	}
	if err := model.ValidateTrack(track); err != nil {
		return model.Track{}, err
	}
	return track, nil
}

func (s *Service) TrackLifecycleView(ctx context.Context, projectID, key string, revision int) (TrackView, error) {
	track, err := s.TrackLifecycleRead(ctx, projectID, key, revision)
	if err != nil {
		return TrackView{}, err
	}
	return s.trackView(ctx, track)
}

func (s *Service) TrackLifecycleUpdate(ctx context.Context, in TrackUpdateInput) (model.Track, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if in.Actor == "" || in.Reason == "" {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track update requires actor and reason")
	}
	stored, err := s.trackReadStored(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	current, err := s.TrackLifecycleRead(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if current.Status != model.TrackPlanned {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track update is planned-only")
	}
	if in.Tasks != nil {
		milestone, milestoneErr := s.MilestoneLifecycleRead(ctx, in.ProjectID, current.Milestone, 0)
		if milestoneErr != nil {
			return model.Track{}, OperationResult{}, milestoneErr
		}
		code, codeErr := s.sharedTaskProjectCode(ctx, in.ProjectID)
		if codeErr != nil {
			return model.Track{}, OperationResult{}, codeErr
		}
		if err := s.validateTrackTasks(ctx, in.ProjectID, code, milestone, *in.Tasks); err != nil {
			return model.Track{}, OperationResult{}, err
		}
		for _, taskID := range *in.Tasks {
			if other, found, findErr := s.nonterminalTrackForTaskExcluding(ctx, in.ProjectID, taskID, current.ID); findErr != nil {
				return model.Track{}, OperationResult{}, findErr
			} else if found {
				return model.Track{}, OperationResult{}, fmt.Errorf("Task %q already belongs to nonterminal Track %q", taskID, other.ID)
			}
		}
	}
	if in.Title == nil && in.Summary == nil && in.Tasks == nil {
		return current, OperationResult{
			ProjectID: current.ProjectID,
			EntityKey: current.ID,
			Revision:  current.Revision,
			Status:    current.Status,
		}, nil
	}
	next := stored
	if in.Title != nil {
		next.Title = *in.Title
	}
	if in.Summary != nil {
		next.Summary = *in.Summary
	}
	if in.Tasks != nil {
		next.Tasks = append([]string{}, (*in.Tasks)...)
	}
	return s.commitTrackContent(ctx, stored, next, "track-update", in.Actor, in.Reason, []string{"title", "summary", "tasks"})
}

func (s *Service) TrackLifecycleAppendTasks(ctx context.Context, in TrackMembershipInput) (model.Track, OperationResult, error) {
	return s.mutateTrackMembership(ctx, in, true)
}

func (s *Service) TrackLifecycleRemoveTasks(ctx context.Context, in TrackMembershipInput) (model.Track, OperationResult, error) {
	return s.mutateTrackMembership(ctx, in, false)
}

func (s *Service) mutateTrackMembership(ctx context.Context, in TrackMembershipInput, appendTasks bool) (model.Track, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if in.Actor == "" || in.Reason == "" || len(in.Tasks) == 0 {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track membership mutation requires tasks, actor, and reason")
	}
	stored, err := s.trackReadStored(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	current, err := s.TrackLifecycleRead(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if current.Status == model.TrackPlanned {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track append/remove is post-start only")
	}
	if stored.Status == model.TrackAccepted || stored.Status == model.TrackCancelled || stored.Status == model.TrackArchived {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track %q is immutable in status %q", in.Key, stored.Status)
	}
	milestone, err := s.MilestoneLifecycleRead(ctx, in.ProjectID, current.Milestone, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	code, err := s.sharedTaskProjectCode(ctx, in.ProjectID)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if err := s.validateTrackTasks(ctx, in.ProjectID, code, milestone, in.Tasks); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	memberSet := make(map[string]struct{}, len(stored.Tasks))
	for _, taskID := range stored.Tasks {
		memberSet[taskID] = struct{}{}
	}
	if appendTasks {
		for _, taskID := range in.Tasks {
			if _, found := memberSet[taskID]; found {
				continue
			}
			if other, found, findErr := s.nonterminalTrackForTaskExcluding(ctx, in.ProjectID, taskID, stored.ID); findErr != nil {
				return model.Track{}, OperationResult{}, findErr
			} else if found {
				return model.Track{}, OperationResult{}, fmt.Errorf("Task %q already belongs to nonterminal Track %q", taskID, other.ID)
			}
			memberSet[taskID] = struct{}{}
		}
	} else {
		for _, taskID := range in.Tasks {
			if _, found := memberSet[taskID]; !found {
				return model.Track{}, OperationResult{}, fmt.Errorf("Task %q is not a Track member", taskID)
			}
			if containsString(stored.DispatchedTasks, taskID) {
				return model.Track{}, OperationResult{}, fmt.Errorf("Track Task %q was already dispatched and cannot be removed", taskID)
			}
			delete(memberSet, taskID)
		}
	}
	if len(memberSet) == 0 {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track must retain at least one Task")
	}
	if len(memberSet) == len(stored.Tasks) && sameStringSet(memberSet, stored.Tasks) {
		return current, OperationResult{
			ProjectID: current.ProjectID,
			EntityKey: current.ID,
			Revision:  current.Revision,
			Status:    current.Status,
		}, nil
	}
	ordered := append([]string{}, stored.Tasks...)
	if appendTasks {
		for _, taskID := range in.Tasks {
			if !containsString(ordered, taskID) {
				ordered = append(ordered, taskID)
			}
		}
	} else {
		filtered := ordered[:0]
		for _, taskID := range ordered {
			if _, remove := memberSet[taskID]; remove {
				filtered = append(filtered, taskID)
			}
		}
		ordered = filtered
	}
	next := stored
	next.Tasks = ordered
	next.Review = nil
	next.Status, err = s.trackStatusFromTasks(ctx, next)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	kind := "track-append-task"
	if !appendTasks {
		kind = "track-remove-task"
	}
	return s.commitTrackContent(ctx, stored, next, kind, in.Actor, in.Reason, []string{"tasks", "review", "status"})
}

func (s *Service) TrackLifecycleCancel(ctx context.Context, in TrackCancelInput) (model.Track, OperationResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, in.ProjectID); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if in.Actor == "" || in.Reason == "" {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track cancellation requires actor and reason")
	}
	stored, err := s.trackReadStored(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	current, err := s.TrackLifecycleRead(ctx, in.ProjectID, in.Key, 0)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if current.Status != model.TrackPlanned {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track cancel is planned-only")
	}
	now := s.durableNow().UTC()
	next := stored
	next.Status = model.TrackCancelled
	next.CancelledAt = &now
	next.CancelledBy = in.Actor
	next.CancelledReason = in.Reason
	next.UpdatedBy = in.Actor
	next.UpdatedAt = now
	next.Revision++
	committed, result, err := s.commitTrackEvent(ctx, stored, next, "track-cancel", in.Actor, in.Reason, []string{"status", "cancelled_at", "cancelled_by", "cancelled_reason"})
	return committed, result, err
}

func (s *Service) TrackLifecycleSubmit(ctx context.Context, projectID, key, actor string) (TrackReviewResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return TrackReviewResult{}, err
	}
	if actor == "" {
		return TrackReviewResult{}, fmt.Errorf("Track submitter is required")
	}
	stored, err := s.trackReadStored(ctx, projectID, key, 0)
	if err != nil {
		return TrackReviewResult{}, err
	}
	current, err := s.TrackLifecycleRead(ctx, projectID, key, 0)
	if err != nil {
		return TrackReviewResult{}, err
	}
	if current.Status != model.TrackReady && current.Status != model.TrackStale {
		return TrackReviewResult{}, fmt.Errorf("Track %q is not ready for review: status %q", key, current.Status)
	}
	next := stored
	next.Status = model.TrackReviewPending
	next.Revision++
	review, err := s.trackSnapshot(ctx, next)
	if err != nil {
		return TrackReviewResult{}, err
	}
	review.SubmittedBy = actor
	review.SubmittedAt = s.durableNow().UTC()
	if !review.SubmittedAt.After(stored.UpdatedAt) {
		review.SubmittedAt = stored.UpdatedAt.Add(time.Nanosecond)
	}
	next.Review = &review
	next.UpdatedBy = actor
	next.UpdatedAt = review.SubmittedAt
	if _, _, err := s.commitTrackEvent(ctx, stored, next, "track-submit", actor, "submit Track for Planner acceptance", []string{"status", "review"}); err != nil {
		return TrackReviewResult{}, err
	}
	view, err := s.trackView(ctx, next)
	if err != nil {
		return TrackReviewResult{}, err
	}
	return TrackReviewResult{
		Track: next,
		View:  view,
	}, nil
}

func (s *Service) TrackLifecycleAccept(ctx context.Context, projectID, key, actor string) (TrackReviewResult, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return TrackReviewResult{}, err
	}
	if actor == "" {
		return TrackReviewResult{}, fmt.Errorf("Track accepter is required")
	}
	stored, err := s.trackReadStored(ctx, projectID, key, 0)
	if err != nil {
		return TrackReviewResult{}, err
	}
	current, err := s.TrackLifecycleRead(ctx, projectID, key, 0)
	if err != nil {
		return TrackReviewResult{}, err
	}
	if current.Status != model.TrackReviewPending {
		return TrackReviewResult{}, fmt.Errorf("Track %q is not pending fresh review: status %q", key, current.Status)
	}
	fresh, err := s.trackReviewIsFresh(ctx, stored)
	if err != nil {
		return TrackReviewResult{}, err
	}
	if !fresh {
		return TrackReviewResult{}, fmt.Errorf("Track %q review snapshot is stale", key)
	}
	next := stored
	next.Status = model.TrackAccepted
	next.UpdatedBy = actor
	next.UpdatedAt = s.durableNow().UTC()
	if !next.UpdatedAt.After(stored.UpdatedAt) {
		next.UpdatedAt = stored.UpdatedAt.Add(time.Nanosecond)
	}
	if _, _, err := s.commitTrackEvent(ctx, stored, next, "track-accept", actor, "accept fresh Track review", []string{"status"}); err != nil {
		return TrackReviewResult{}, err
	}
	view, err := s.trackView(ctx, next)
	if err != nil {
		return TrackReviewResult{}, err
	}
	return TrackReviewResult{
		Track: next,
		View:  view,
	}, nil
}

func (s *Service) TrackLifecycleArchive(ctx context.Context, projectID, key, actor, reason string) (model.Track, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return model.Track{}, err
	}
	if actor == "" || reason == "" {
		return model.Track{}, fmt.Errorf("Track archive requires actor and reason")
	}
	stored, err := s.trackReadStored(ctx, projectID, key, 0)
	if err != nil {
		return model.Track{}, err
	}
	current, err := s.TrackLifecycleRead(ctx, projectID, key, 0)
	if err != nil {
		return model.Track{}, err
	}
	if current.Status != model.TrackAccepted && current.Status != model.TrackCancelled {
		return model.Track{}, fmt.Errorf("Track %q cannot be archived from status %q", key, current.Status)
	}
	next := stored
	next.Status = model.TrackArchived
	next.UpdatedBy = actor
	next.UpdatedAt = s.durableNow().UTC()
	if !next.UpdatedAt.After(stored.UpdatedAt) {
		next.UpdatedAt = stored.UpdatedAt.Add(time.Nanosecond)
	}
	if _, _, err := s.commitTrackEvent(ctx, stored, next, "track-archive", actor, reason, []string{"status"}); err != nil {
		return model.Track{}, err
	}
	return next, nil
}

func (s *Service) TrackLifecycleListQuery(ctx context.Context, projectID, text, status, milestone, cursor string, includeArchived bool) (TrackPage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return TrackPage{}, err
	}
	filters := map[string]string{}
	if milestone != "" {
		filters["milestone"] = milestone
	}
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "track", ProjectID: projectID, Text: text, Filters: filters, IncludeArchived: includeArchived || status == model.TrackArchived, Limit: sqlitestore.SharedLifecycleQueryMaxRows, Cursor: cursor})
	if err != nil {
		return TrackPage{}, err
	}
	result := TrackPage{
		Tracks:     make([]TrackView, 0, len(page.Entities)),
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
		CursorKind: page.CursorKind,
	}
	for _, entity := range page.Entities {
		var track model.Track
		if err := json.Unmarshal(entity.Payload, &track); err != nil {
			return TrackPage{}, err
		}
		if track.ID != entity.ID || track.ProjectID != projectID {
			return TrackPage{}, fmt.Errorf("shared Track identity mismatch")
		}
		if err := model.ValidateTrack(track); err != nil {
			return TrackPage{}, err
		}
		if track.Status != model.TrackArchived {
			derived, deriveErr := s.deriveTrackStatus(ctx, track)
			if deriveErr != nil {
				return TrackPage{}, deriveErr
			}
			track.Status = derived
		}
		if status != "" && track.Status != status {
			continue
		}
		view, viewErr := s.trackView(ctx, track)
		if viewErr != nil {
			return TrackPage{}, viewErr
		}
		result.Tracks = append(result.Tracks, view)
	}
	return result, nil
}

func (s *Service) TrackLifecycleHistory(ctx context.Context, projectID, key, cursor string) (sqlitestore.SharedHistoryPage, error) {
	if err := s.requireLocalTaskAuthoring(ctx, projectID); err != nil {
		return sqlitestore.SharedHistoryPage{}, err
	}
	after := sqlitestore.SharedLifecycleHistoryCursor{}
	kind := "track-history:" + projectID + ":" + key
	if cursor != "" {
		decoded, err := pagination.DecodeOpaqueKeyset(cursor, kind)
		if err != nil {
			return sqlitestore.SharedHistoryPage{}, fmt.Errorf("invalid Track history cursor")
		}
		after, err = sqlitestore.DecodeSharedLifecycleHistoryCursor(decoded)
		if err != nil {
			return sqlitestore.SharedHistoryPage{}, fmt.Errorf("invalid Track history cursor")
		}
	}
	return s.Durability.ListSharedLifecycleHistoryPage(ctx, "track", projectID, key, after, sqlitestore.SharedLifecycleQueryMaxRows)
}

func (s *Service) validateTrackTasks(ctx context.Context, projectID, projectCode string, milestone model.Milestone, taskIDs []string) error {
	if len(taskIDs) == 0 || len(taskIDs) > model.TrackMaxTasks {
		return fmt.Errorf("Track task membership must contain 1-%d Tasks", model.TrackMaxTasks)
	}
	milestoneTasks := make(map[string]struct{}, len(milestone.Tasks))
	for _, taskID := range milestone.Tasks {
		milestoneTasks[taskID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		if err := model.ValidateTaskIDForProject(taskID, projectCode); err != nil {
			return fmt.Errorf("invalid Track task %q: %w", taskID, err)
		}
		if _, exists := seen[taskID]; exists {
			return fmt.Errorf("duplicate Track task %q", taskID)
		}
		if _, member := milestoneTasks[taskID]; !member {
			return fmt.Errorf("Track task %q is not a member of Milestone %q", taskID, milestone.ID)
		}
		if _, err := s.readSharedTask(ctx, projectID, taskID); err != nil {
			return fmt.Errorf("Track task %q does not resolve: %w", taskID, err)
		}
		seen[taskID] = struct{}{}
	}
	return nil
}

func (s *Service) deriveTrackStatus(ctx context.Context, track model.Track) (string, error) {
	switch track.Status {
	case model.TrackArchived, model.TrackCancelled:
		return track.Status, nil
	case model.TrackAccepted, model.TrackReviewPending:
		fresh, err := s.trackReviewIsFresh(ctx, track)
		if err != nil {
			return "", err
		}
		if !fresh {
			return model.TrackStale, nil
		}
		return track.Status, nil
	case model.TrackStale:
		return model.TrackStale, nil
	}
	return s.trackStatusFromTasks(ctx, track)
}

func (s *Service) trackStatusFromTasks(ctx context.Context, track model.Track) (string, error) {
	started := false
	ready := true
	for _, taskID := range track.Tasks {
		state, found, err := s.Durability.ReadTaskExecutionState(ctx, track.ProjectID, taskID)
		if err != nil {
			return "", err
		}
		if !found {
			ready = false
			continue
		}
		started = true
		if state.Status != model.TaskExecutionIntegrated && state.Status != model.TaskExecutionDone {
			ready = false
		}
	}
	if ready && len(track.Tasks) > 0 {
		return model.TrackReady, nil
	}
	if started {
		return model.TrackActive, nil
	}
	return model.TrackPlanned, nil
}

func (s *Service) trackSnapshot(ctx context.Context, track model.Track) (model.TrackReview, error) {
	project, err := s.EffectiveProjectConfig(track.ProjectID)
	if err != nil {
		return model.TrackReview{}, err
	}
	head, err := s.Git.RefreshDefaultBranch(ctx, project)
	if err != nil {
		return model.TrackReview{}, err
	}
	tree, err := s.Git.CommitTree(ctx, project, head)
	if err != nil {
		return model.TrackReview{}, err
	}
	tasks := make([]model.TrackTaskSnapshot, 0, len(track.Tasks))
	for _, taskID := range track.Tasks {
		task, taskErr := s.readSharedTask(ctx, track.ProjectID, taskID)
		if taskErr != nil {
			return model.TrackReview{}, taskErr
		}
		tasks = append(tasks, model.TrackTaskSnapshot{Key: task.ID, Revision: task.Revision, RevisionSHA256: task.RevisionSHA256})
	}
	canonical := struct {
		Head          string                    `json:"head"`
		Tree          string                    `json:"tree"`
		TrackRevision int                       `json:"track_revision"`
		Tasks         []model.TrackTaskSnapshot `json:"tasks"`
	}{head, tree, track.Revision, tasks}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return model.TrackReview{}, err
	}
	digest := sha256.Sum256(raw)
	return model.TrackReview{Head: head, Tree: tree, Digest: hex.EncodeToString(digest[:]), TrackRevision: track.Revision, Tasks: tasks}, nil
}

func (s *Service) trackReviewIsFresh(ctx context.Context, track model.Track) (bool, error) {
	if track.Review == nil {
		return false, nil
	}
	candidate, err := s.trackSnapshot(ctx, track)
	if err != nil {
		return false, err
	}
	return candidate.TrackRevision == track.Review.TrackRevision && candidate.Head == track.Review.Head && candidate.Tree == track.Review.Tree && candidate.Digest == track.Review.Digest && reflect.DeepEqual(candidate.Tasks, track.Review.Tasks), nil
}

func (s *Service) trackView(ctx context.Context, track model.Track) (TrackView, error) {
	items := make([]TrackTaskProjection, 0, len(track.Tasks))
	for _, taskID := range track.Tasks {
		task, err := s.readSharedTask(ctx, track.ProjectID, taskID)
		if err != nil {
			return TrackView{}, err
		}
		status := task.Status
		if state, found, stateErr := s.Durability.ReadTaskExecutionState(ctx, track.ProjectID, taskID); stateErr != nil {
			return TrackView{}, stateErr
		} else if found {
			status = state.Status
		}
		items = append(items, TrackTaskProjection{
			Key:          task.ID,
			Title:        task.Title,
			Priority:     task.Priority,
			Dependencies: append([]string{}, task.Dependencies...),
			Status:       status,
			Revision:     task.Revision,
		})
	}
	return TrackView{
		Track: track,
		Tasks: items,
	}, nil
}

func (s *Service) commitTrackContent(ctx context.Context, previous, next model.Track, kind, actor, reason string, changed []string) (model.Track, OperationResult, error) {
	now := s.durableNow().UTC()
	next.Revision = previous.Revision + 1
	next.UpdatedBy = actor
	next.UpdatedAt = now
	if !next.UpdatedAt.After(previous.UpdatedAt) {
		next.UpdatedAt = previous.UpdatedAt.Add(time.Nanosecond)
	}
	if err := model.ValidateTrack(next); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	return s.commitTrack(ctx, previous, next, kind, actor, reason, changed)
}

func (s *Service) commitTrackEvent(ctx context.Context, previous, next model.Track, kind, actor, reason string, changed []string) (model.Track, OperationResult, error) {
	if err := model.ValidateTrack(next); err != nil {
		return model.Track{}, OperationResult{}, err
	}
	committed, result, err := s.commitTrack(ctx, previous, next, kind, actor, reason, changed)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	if result.EntityKey == "" {
		return model.Track{}, OperationResult{}, fmt.Errorf("Track mutation did not commit")
	}
	return committed, result, nil
}

func (s *Service) commitTrack(ctx context.Context, previous, next model.Track, kind, actor, reason string, changed []string) (model.Track, OperationResult, error) {
	payload, err := json.Marshal(next)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "track", previous.ID)
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	op := kind + "-" + previous.ID + "-r" + strconv.Itoa(next.Revision)
	var receipt sqlitestore.SharedMutationReceipt
	if previous.Status != next.Status {
		receipt, err = s.Durability.CommitSharedLifecycleEvent(ctx, sqlitestore.SharedLifecycleEventRequest{OperationID: op, EntityType: "track", ProjectID: previous.ProjectID, EntityID: previous.ID, ExpectedRevision: int64(previous.Revision), ExpectedStoreRevision: entity.Revision, ExpectedPayload: entity.Payload, Revision: int64(next.Revision), Kind: kind, EventKind: sqlitestore.SharedLifecycleEventKindStatus, HistoryMutationKind: "status", FromStatus: previous.Status, ToStatus: next.Status, Payload: payload, Actor: actor, Reason: reason, ChangedFields: changed, Contract: []byte(`{"entity":"track"}`), CreatedAt: next.UpdatedAt})
	} else {
		receipt, err = s.Durability.CommitSharedLifecycleRevision(ctx, sqlitestore.SharedLifecycleRevision{OperationID: op, EntityType: "track", ProjectID: previous.ProjectID, EntityID: previous.ID, ExpectedRevision: int64(previous.Revision), ExpectedStoreRevision: entity.Revision, Revision: int64(next.Revision), Kind: kind, HistoryMutationKind: "update", Payload: payload, Actor: actor, Reason: reason, ChangedFields: changed, CreatedAt: next.UpdatedAt})
	}
	if err != nil {
		return model.Track{}, OperationResult{}, err
	}
	return next, OperationResult{
		OperationID: receipt.OperationID,
		ProjectID:   next.ProjectID,
		EntityKey:   next.ID,
		Revision:    next.Revision,
		Status:      next.Status,
	}, nil
}

func (s *Service) recordTrackTaskDispatch(ctx context.Context, projectID, taskID string) error {
	track, found, err := s.nonterminalTrackForTask(ctx, projectID, taskID)
	if err != nil || !found {
		return err
	}
	if track.Status == model.TrackAccepted {
		return fmt.Errorf("Task %q belongs to an accepted Track whose review is stale", taskID)
	}
	if containsString(track.DispatchedTasks, taskID) {
		return nil
	}
	next := track
	next.DispatchedTasks = append(append([]string{}, track.DispatchedTasks...), taskID)
	next.Status, err = s.trackStatusFromTasks(ctx, next)
	if err != nil {
		return err
	}
	next.Review = nil
	_, _, err = s.commitTrackContent(ctx, track, next, "track-dispatch", "gateway", "Task dispatched", []string{"status", "dispatched_tasks", "review"})
	return err
}

func (s *Service) nonterminalTrackForTask(ctx context.Context, projectID, taskID string) (model.Track, bool, error) {
	return s.nonterminalTrackForTaskExcluding(ctx, projectID, taskID, "")
}

func (s *Service) nonterminalTrackForTaskExcluding(ctx context.Context, projectID, taskID, excluded string) (model.Track, bool, error) {
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "track", ProjectID: projectID, IncludeArchived: true, Limit: sqlitestore.SharedLifecycleQueryMaxRows})
	if err != nil {
		return model.Track{}, false, err
	}
	for _, entity := range page.Entities {
		if entity.ID == excluded {
			continue
		}
		var track model.Track
		if err := json.Unmarshal(entity.Payload, &track); err != nil {
			return model.Track{}, false, err
		}
		if !containsString(track.Tasks, taskID) {
			continue
		}
		status := track.Status
		if status == model.TrackAccepted {
			status, err = s.deriveTrackStatus(ctx, track)
			if err != nil {
				return model.Track{}, false, err
			}
		}
		switch status {
		case model.TrackPlanned, model.TrackActive, model.TrackReady, model.TrackReviewPending, model.TrackStale:
			return track, true, nil
		}
	}
	return model.Track{}, false, nil
}

func (s *Service) requireMilestoneHasNoNonterminalTracks(ctx context.Context, projectID, milestoneID string) error {
	milestone, err := s.MilestoneLifecycleRead(ctx, projectID, milestoneID, 0)
	if err != nil {
		return err
	}
	members := make(map[string]struct{}, len(milestone.Tasks))
	for _, taskID := range milestone.Tasks {
		members[taskID] = struct{}{}
	}
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "track", ProjectID: projectID, IncludeArchived: true, Limit: sqlitestore.SharedLifecycleQueryMaxRows})
	if err != nil {
		return err
	}
	for _, entity := range page.Entities {
		var track model.Track
		if err := json.Unmarshal(entity.Payload, &track); err != nil {
			return err
		}
		if track.Milestone != milestoneID {
			continue
		}
		referencesMember := false
		for _, taskID := range track.Tasks {
			if _, ok := members[taskID]; ok {
				referencesMember = true
				break
			}
		}
		if !referencesMember {
			continue
		}
		status := track.Status
		if status == model.TrackAccepted {
			status, err = s.deriveTrackStatus(ctx, track)
			if err != nil {
				return err
			}
		}
		switch status {
		case model.TrackPlanned, model.TrackActive, model.TrackReady, model.TrackReviewPending, model.TrackStale:
			return fmt.Errorf("Milestone %q has nonterminal Track %q", milestoneID, track.ID)
		}
	}
	return nil
}

func sameStringSet(set map[string]struct{}, values []string) bool {
	if len(set) != len(values) {
		return false
	}
	for _, value := range values {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
